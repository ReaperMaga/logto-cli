// Package bootstrap creates the first machine-to-machine credential for the
// Logto Management API directly in Postgres, without the admin console.
//
// It writes to Logto internal tables. Table and column names were checked
// against packages/schemas/tables/*.sql (applications, application_secrets,
// applications_roles, roles, roles_scopes, scopes, resources) at the version
// in supportedLogtoVersion. Re-check them before upgrading Logto.
package bootstrap

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"regexp"

	"github.com/jackc/pgx/v5"
)

// supportedLogtoVersion is the Logto release whose schema the SQL below was
// verified against.
const supportedLogtoVersion = "1.44.0"

// SupportedLogtoVersion returns the Logto version the SQL was verified against.
func SupportedLogtoVersion() string { return supportedLogtoVersion }

const (
	tenantID           = "default"
	managementResource = "https://default.logto.app/api"
	secretName         = "bootstrap"
	// DefaultName is the default display name of the bootstrap app.
	DefaultName = "nomad-bootstrap"
)

var (
	appIDPattern = regexp.MustCompile(`^[a-z0-9]{1,21}$`)
	// ErrNotSeeded means the Management API M2M role does not exist.
	ErrNotSeeded = errors.New("management API M2M role (scope 'all' on " + managementResource + ") not found: Logto not seeded yet?")
)

// Options configures a bootstrap run.
type Options struct {
	DBURL     string
	AppID     string
	AppSecret string
	Name      string
	// Rotate overwrites the stored secret when the app exists with a different one.
	Rotate bool
}

// Result describes what a run changed.
type Result struct {
	RoleID         string
	AppCreated     bool
	SecretCreated  bool
	SecretRotated  bool
	RoleLinked     bool
	SecretMismatch bool // the app exists with a different secret and Rotate was false
}

// Changed reports whether the run wrote anything.
func (r Result) Changed() bool {
	return r.AppCreated || r.SecretCreated || r.SecretRotated || r.RoleLinked
}

// Validate checks the ID and secret against the column constraints.
func Validate(appID, secret string) error {
	if !appIDPattern.MatchString(appID) {
		return fmt.Errorf("invalid app ID %q: must be 1-21 characters of [a-z0-9]", appID)
	}
	if secret == "" || len(secret) > 64 {
		return errors.New("invalid app secret: must be 1-64 characters")
	}
	return nil
}

// Run creates (or confirms) the M2M app, its secret and its link to the
// Management API role, all in one transaction.
func Run(ctx context.Context, opts Options) (Result, error) {
	var res Result
	if opts.DBURL == "" {
		return res, errors.New("missing database URL (--db-url / LOGTO_DB_URL / DB_URL)")
	}
	if err := Validate(opts.AppID, opts.AppSecret); err != nil {
		return res, err
	}
	if opts.Name == "" {
		opts.Name = DefaultName
	}

	conn, err := pgx.Connect(ctx, opts.DBURL)
	if err != nil {
		return res, fmt.Errorf("connect to database: %w", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return res, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if res, err = run(ctx, tx, opts); err != nil {
		return res, err
	}
	if err := tx.Commit(ctx); err != nil {
		return res, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}

func run(ctx context.Context, tx pgx.Tx, opts Options) (Result, error) {
	var res Result

	// 1. The seeded "Pre-configured Management API access" M2M role.
	err := tx.QueryRow(ctx, `
		SELECT r.id FROM roles r
		JOIN roles_scopes rs ON rs.role_id = r.id
		JOIN scopes s        ON s.id = rs.scope_id
		JOIN resources res   ON res.id = s.resource_id
		WHERE r.tenant_id = $1 AND r.type = 'MachineToMachine'
		  AND res.indicator = $2 AND s.name = 'all'
		ORDER BY r.id
		LIMIT 1`, tenantID, managementResource).Scan(&res.RoleID)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, ErrNotSeeded
	}
	if err != nil {
		return res, fmt.Errorf("look up management API role: %w", err)
	}

	// 2. The application. An existing app with this ID must be an M2M app.
	var existingType, legacySecret string
	err = tx.QueryRow(ctx,
		`SELECT type::text, secret FROM applications WHERE tenant_id = $1 AND id = $2`,
		tenantID, opts.AppID).Scan(&existingType, &legacySecret)
	appExists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return res, fmt.Errorf("look up application: %w", err)
	}

	if !appExists {
		// applications.secret is deprecated but NOT NULL; fill it as well.
		tag, err := tx.Exec(ctx, `
			INSERT INTO applications (tenant_id, id, name, secret, type, oidc_client_metadata)
			VALUES ($1, $2, $3, $4, 'MachineToMachine', '{"redirectUris":[],"postLogoutRedirectUris":[]}')
			ON CONFLICT (id) DO NOTHING`,
			tenantID, opts.AppID, opts.Name, opts.AppSecret)
		if err != nil {
			return res, fmt.Errorf("insert application: %w", err)
		}
		res.AppCreated = tag.RowsAffected() == 1
	} else if existingType != "MachineToMachine" {
		return res, fmt.Errorf("application %q exists but has type %s, not MachineToMachine", opts.AppID, existingType)
	}

	// 3. The secret.
	secretKnown := !appExists || legacySecret == opts.AppSecret
	if appExists && !secretKnown {
		var n int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM application_secrets WHERE tenant_id = $1 AND application_id = $2 AND value = $3`,
			tenantID, opts.AppID, opts.AppSecret).Scan(&n); err != nil {
			return res, fmt.Errorf("look up application secrets: %w", err)
		}
		secretKnown = n > 0
	}

	switch {
	case secretKnown:
		tag, err := tx.Exec(ctx, `
			INSERT INTO application_secrets (tenant_id, application_id, name, value)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT DO NOTHING`,
			tenantID, opts.AppID, secretName, opts.AppSecret)
		if err != nil {
			return res, fmt.Errorf("insert application secret: %w", err)
		}
		res.SecretCreated = tag.RowsAffected() == 1
	case opts.Rotate:
		if _, err := tx.Exec(ctx,
			`UPDATE applications SET secret = $3 WHERE tenant_id = $1 AND id = $2`,
			tenantID, opts.AppID, opts.AppSecret); err != nil {
			return res, fmt.Errorf("rotate application secret: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO application_secrets (tenant_id, application_id, name, value)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (tenant_id, application_id, name) DO UPDATE SET value = EXCLUDED.value`,
			tenantID, opts.AppID, secretName, opts.AppSecret); err != nil {
			return res, fmt.Errorf("rotate application secret: %w", err)
		}
		res.SecretRotated = true
	default:
		res.SecretMismatch = true
	}

	// 4. Link the app to the Management API role.
	linkID, err := RandomID()
	if err != nil {
		return res, err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO applications_roles (tenant_id, id, application_id, role_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT DO NOTHING`,
		tenantID, linkID, opts.AppID, res.RoleID)
	if err != nil {
		return res, fmt.Errorf("link application to role: %w", err)
	}
	res.RoleLinked = tag.RowsAffected() == 1

	return res, nil
}

const (
	idAlphabet     = "abcdefghijklmnopqrstuvwxyz0123456789"
	secretAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

// RandomID returns a 21-character lowercase alphanumeric ID, like Logto's own.
func RandomID() (string, error) { return randomString(idAlphabet, 21) }

// RandomSecret returns a 32-character alphanumeric secret, like Logto's own.
func RandomSecret() (string, error) { return randomString(secretAlphabet, 32) }

func randomString(alphabet string, n int) (string, error) {
	out := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range out {
		k, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("generate random string: %w", err)
		}
		out[i] = alphabet[k.Int64()]
	}
	return string(out), nil
}
