package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/reapermaga/logto-cli/internal/bootstrap"
)

func (a *app) bootstrapCmd() *cobra.Command {
	var (
		name     string
		generate bool
		rotate   bool
	)
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Create the first M2M credential directly in Postgres",
		Long: `Creates a MachineToMachine app linked to Logto's seeded "Management API access"
role, in a single transaction against Logto's database. Idempotent: a second run
with the same input changes nothing.

Writes Logto internal tables; verified against Logto ` + bootstrap.SupportedLogtoVersion() + `.`,
		Example: `  logto-cli bootstrap --db-url "$DB_URL" --app-id myid --app-secret mysecret
  logto-cli bootstrap --db-url "$DB_URL" --generate`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			appID, secret := a.cfg.appID, a.cfg.appSecret
			if generate {
				if cmd.Flags().Changed("app-id") || cmd.Flags().Changed("app-secret") {
					return errors.New("--generate cannot be combined with --app-id or --app-secret")
				}
				var err error
				if appID, err = bootstrap.RandomID(); err != nil {
					return withCode(ExitBootstrap, err)
				}
				if secret, err = bootstrap.RandomSecret(); err != nil {
					return withCode(ExitBootstrap, err)
				}
			} else if appID == "" || secret == "" {
				return errors.New("--app-id and --app-secret are required (or use --generate)")
			}

			res, err := bootstrap.Run(cmd.Context(), bootstrap.Options{
				DBURL:     a.cfg.dbURL,
				AppID:     appID,
				AppSecret: secret,
				Name:      name,
				Rotate:    rotate,
			})
			if err != nil {
				return withCode(ExitBootstrap, err)
			}

			if generate {
				// The generated credential is the purpose of this command: print it once.
				_, _ = fmt.Fprintf(a.stdout, "LOGTO_APP_ID=%s\nLOGTO_APP_SECRET=%s\n", appID, secret)
			}
			a.reportBootstrap(appID, res)

			if a.cfg.endpoint == "" {
				return nil
			}
			if res.SecretMismatch {
				a.infof("Skipping API verification: the given secret is not the stored one.")
				return nil
			}
			a.cfg.appID, a.cfg.appSecret = appID, secret
			c, err := a.api()
			if err != nil {
				return err
			}
			if _, err := c.GetApplication(cmd.Context(), appID); err != nil {
				return fmt.Errorf("verification against %s failed: %w", a.cfg.endpoint, err)
			}
			a.infof("Verified: fetched a token and read application %s from %s.", appID, a.cfg.endpoint)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", bootstrap.DefaultName, "display name of the M2M app")
	f.BoolVar(&generate, "generate", false, "generate the app ID and secret and print them once")
	f.BoolVar(&rotate, "rotate", false, "overwrite the secret if the app exists with a different one")
	return cmd
}

func (a *app) reportBootstrap(appID string, res bootstrap.Result) {
	if res.SecretMismatch {
		a.warnf("application %s exists with a different secret; left unchanged (use --rotate to overwrite)", appID)
	}
	if !res.Changed() {
		a.infof("No changes: application %s is already bootstrapped (role %s).", appID, res.RoleID)
		return
	}
	if res.AppCreated {
		a.infof("Created M2M application %s.", appID)
	}
	if res.SecretCreated {
		a.infof("Stored secret %q.", "bootstrap")
	}
	if res.SecretRotated {
		a.infof("Rotated the application secret.")
	}
	if res.RoleLinked {
		a.infof("Linked application to Management API role %s.", res.RoleID)
	}
}
