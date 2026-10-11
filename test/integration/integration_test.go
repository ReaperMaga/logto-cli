//go:build integration

// Package integration runs the CLI end to end against a real Logto started
// with docker-compose.test.yml:
//
//	docker compose -f docker-compose.test.yml up -d --wait
//	go test -tags integration ./test/integration/ -v
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/reapermaga/logto-cli/internal/cli"
)

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

var (
	endpoint = env("LOGTO_IT_ENDPOINT", "http://localhost:3001")
	dbURL    = env("LOGTO_IT_DB_URL", "postgres://postgres:p0stgr3s@localhost:5432/logto")
)

type result struct {
	code           int
	stdout, stderr string
}

// logtoCLI runs the CLI in-process with a clean LOGTO_* environment.
func logtoCLI(t *testing.T, stdin string, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), args, strings.NewReader(stdin), &stdout, &stderr)
	r := result{code, stdout.String(), stderr.String()}
	t.Logf("logto-cli %s -> %d\n%s%s", strings.Join(args, " "), code, r.stdout, r.stderr)
	return r
}

func mustOK(t *testing.T, r result) result {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	return r
}

func waitForLogto(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint+"/api/status", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 300 {
				return
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("Logto at %s did not become healthy", endpoint)
}

func TestEndToEnd(t *testing.T) {
	for _, k := range []string{"LOGTO_ENDPOINT", "LOGTO_APP_ID", "LOGTO_APP_SECRET", "LOGTO_DB_URL", "DB_URL", "LOGTO_OUTPUT", "LOGTO_RESOURCE"} {
		t.Setenv(k, "")
	}
	waitForLogto(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)

	// 1. Bootstrap with generated credentials.
	r := mustOK(t, logtoCLI(t, "", "bootstrap", "--db-url", dbURL, "--generate", "--endpoint", endpoint))
	appID := regexp.MustCompile(`(?m)^LOGTO_APP_ID=(\S+)$`).FindStringSubmatch(r.stdout)
	secret := regexp.MustCompile(`(?m)^LOGTO_APP_SECRET=(\S+)$`).FindStringSubmatch(r.stdout)
	if appID == nil || secret == nil {
		t.Fatalf("bootstrap did not print credentials: %s", r.stdout)
	}
	if !strings.Contains(r.stderr, "Verified") {
		t.Fatalf("bootstrap did not verify against the API: %s", r.stderr)
	}
	t.Setenv("LOGTO_ENDPOINT", endpoint)
	t.Setenv("LOGTO_APP_ID", appID[1])
	t.Setenv("LOGTO_APP_SECRET", secret[1])

	// 2. Auth and raw API.
	r = mustOK(t, logtoCLI(t, "", "auth", "check"))
	if !strings.HasPrefix(r.stdout, "OK") {
		t.Fatalf("auth check: %s", r.stdout)
	}
	r = mustOK(t, logtoCLI(t, "", "api", "GET", "/api/applications"))
	if !strings.Contains(r.stdout, appID[1]) {
		t.Fatalf("api GET /api/applications does not list the bootstrap app")
	}
	if r := logtoCLI(t, "", "api", "GET", "/api/users/does-not-exist"); r.code != cli.ExitError {
		t.Fatalf("api 404 exit %d, want %d", r.code, cli.ExitError)
	}

	// 3. User lifecycle with a role.
	roleName := "it-role-" + suffix
	mustOK(t, logtoCLI(t, "", "roles", "create", "--name", roleName, "--type", "user"))
	username := "ituser" + suffix
	r = mustOK(t, logtoCLI(t, "S3cure-passw0rd!\n", "-o", "json", "users", "create",
		"--username", username, "--email", username+"@example.com", "--password-stdin"))
	var user struct{ ID string }
	if err := json.Unmarshal([]byte(r.stdout), &user); err != nil || user.ID == "" {
		t.Fatalf("users create JSON: %v %s", err, r.stdout)
	}
	mustOK(t, logtoCLI(t, "", "users", "roles", "add", username, roleName))
	r = mustOK(t, logtoCLI(t, "", "users", "roles", "list", user.ID))
	if !strings.Contains(r.stdout, roleName) {
		t.Fatalf("role not assigned")
	}
	mustOK(t, logtoCLI(t, "", "users", "suspend", username+"@example.com"))
	r = mustOK(t, logtoCLI(t, "", "-o", "json", "users", "get", user.ID))
	if !strings.Contains(r.stdout, `"isSuspended": true`) {
		t.Fatalf("user not suspended: %s", r.stdout)
	}
	mustOK(t, logtoCLI(t, "", "users", "delete", user.ID, "--yes"))
	mustOK(t, logtoCLI(t, "", "roles", "delete", roleName, "--yes"))

	// 4. Traditional app: create, read secret, delete.
	r = mustOK(t, logtoCLI(t, "", "-o", "json", "apps", "create", "--name", "it-app-"+suffix,
		"--type", "traditional", "--redirect-uri", "https://example.com/callback"))
	var created struct {
		Application struct{ ID string }    `json:"application"`
		Secret      struct{ Value string } `json:"secret"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &created); err != nil || created.Application.ID == "" || created.Secret.Value == "" {
		t.Fatalf("apps create did not return ID and secret: %v %s", err, r.stdout)
	}
	r = mustOK(t, logtoCLI(t, "", "apps", "secrets", "list", created.Application.ID))
	if !strings.Contains(r.stdout, created.Secret.Value) {
		t.Fatalf("secret not listed")
	}
	mustOK(t, logtoCLI(t, "", "apps", "delete", created.Application.ID, "--yes"))

	// 5. Declarative apply is idempotent.
	spec := fmt.Sprintf(`
roles:
  - name: it-apply-%[1]s
users:
  - username: itapply%[1]s
    roles: [it-apply-%[1]s]
apps:
  - name: it-apply-%[1]s
    type: spa
    redirectUris: [https://example.com/cb]
`, suffix)
	mustOK(t, logtoCLI(t, spec, "apply", "-f", "-"))
	r = mustOK(t, logtoCLI(t, spec, "apply", "-f", "-"))
	if !strings.Contains(r.stdout, "No changes.") {
		t.Fatalf("second apply was not a no-op: %s", r.stdout)
	}
	mustOK(t, logtoCLI(t, "", "users", "delete", "itapply"+suffix, "--yes"))
	mustOK(t, logtoCLI(t, "", "apps", "delete", "it-apply-"+suffix, "--yes"))
	mustOK(t, logtoCLI(t, "", "roles", "delete", "it-apply-"+suffix, "--yes"))

	// 6. A second bootstrap with the same credential is a no-op.
	r = mustOK(t, logtoCLI(t, "", "bootstrap", "--db-url", dbURL))
	if !strings.Contains(r.stderr, "No changes") {
		t.Fatalf("second bootstrap was not a no-op: %s", r.stderr)
	}
	// A different secret is refused without --rotate.
	r = mustOK(t, logtoCLI(t, "", "bootstrap", "--db-url", dbURL, "--app-secret", "different"))
	if !strings.Contains(r.stderr, "different secret") {
		t.Fatalf("secret mismatch not reported: %s", r.stderr)
	}
}
