package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oidc/token", func(w http.ResponseWriter, r *http.Request) {
		if id, secret, _ := r.BasicAuth(); id != "app" || secret != "secret" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"error":"invalid_client"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"access_token":"tok","expires_in":3600}`)
	})
	mux.HandleFunc("GET /api/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Total-Number", "1")
		_, _ = fmt.Fprint(w, `[{"id":"u1","username":"alice","primaryEmail":"alice@example.com","extra":true}]`)
	})
	mux.HandleFunc("GET /api/applications", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Total-Number", "0")
		_, _ = fmt.Fprint(w, `[]`)
	})
	mux.HandleFunc("GET /api/missing", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"code":"entity.not_found","message":"Not found"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestUsersListJSONFromEnv(t *testing.T) {
	srv := newServer(t)
	t.Setenv("LOGTO_ENDPOINT", srv.URL)
	t.Setenv("LOGTO_APP_ID", "app")
	t.Setenv("LOGTO_APP_SECRET", "secret")
	t.Setenv("LOGTO_OUTPUT", "json")

	code, out, errOut := run(t, "users", "list")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var users []map[string]any
	if err := json.Unmarshal([]byte(out), &users); err != nil {
		t.Fatalf("not JSON: %s", out)
	}
	if users[0]["extra"] != true {
		t.Fatalf("raw API fields must be preserved: %s", out)
	}

	// The flag beats the env var.
	code, out, _ = run(t, "users", "list", "-o", "table")
	if code != ExitOK || !strings.Contains(out, "USERNAME") || !strings.Contains(out, "alice") {
		t.Fatalf("exit %d, table output:\n%s", code, out)
	}
}

func TestExitCodes(t *testing.T) {
	srv := newServer(t)
	base := []string{"--endpoint", srv.URL, "--app-id", "app"}

	code, _, errOut := run(t, append(base, "--app-secret", "wrong", "auth", "check")...)
	if code != ExitAuth {
		t.Fatalf("bad secret: exit %d, want %d (%s)", code, ExitAuth, errOut)
	}
	if strings.Contains(errOut, "wrong") {
		t.Fatalf("secret leaked in error output: %s", errOut)
	}

	code, out, errOut := run(t, append(base, "--app-secret", "secret", "api", "GET", "/api/missing")...)
	if code != ExitError || !strings.Contains(out, "entity.not_found") || !strings.Contains(errOut, "HTTP 404") {
		t.Fatalf("404: exit %d, stdout %q, stderr %q", code, out, errOut)
	}

	code, _, _ = run(t, append(base, "--app-secret", "secret", "auth", "check")...)
	if code != ExitOK {
		t.Fatalf("auth check: exit %d", code)
	}

	code, _, errOut = run(t, "users", "list", "--endpoint", "", "--app-id", "", "--app-secret", "")
	if code != ExitError || !strings.Contains(errOut, "missing configuration") {
		t.Fatalf("missing config: exit %d, %s", code, errOut)
	}

	code, _, errOut = run(t, "bootstrap", "--db-url", "", "--app-id", "x", "--app-secret", "y")
	if code != ExitBootstrap {
		t.Fatalf("bootstrap without DB: exit %d, %s", code, errOut)
	}
}

func TestDeleteRequiresConfirmation(t *testing.T) {
	srv := newServer(t)
	code, _, errOut := run(t, "--endpoint", srv.URL, "--app-id", "app", "--app-secret", "secret", "users", "delete", "alice@example.com")
	if code != ExitError || !strings.Contains(errOut, "--yes") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
}
