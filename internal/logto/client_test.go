package logto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeServer serves /oidc/token plus a handler for /api/*.
type fakeServer struct {
	*httptest.Server
	tokenCalls atomic.Int32
	expiresIn  int
}

func newFakeServer(t *testing.T, api http.HandlerFunc) *fakeServer {
	t.Helper()
	fs := &fakeServer{expiresIn: 3600}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oidc/token", func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		if !ok || id != "app" || secret != "secret" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"error":"invalid_client","error_description":"client authentication failed"}`)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("resource") != DefaultResource || r.Form.Get("scope") != "all" {
			t.Errorf("unexpected token form: %v", r.Form)
		}
		n := fs.tokenCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "token-" + strconv.Itoa(int(n)),
			"expires_in":   fs.expiresIn,
			"token_type":   "Bearer",
		})
	})
	mux.HandleFunc("/api/", api)
	fs.Server = httptest.NewServer(mux)
	t.Cleanup(fs.Close)
	return fs
}

func newTestClient(t *testing.T, url string) *Client {
	t.Helper()
	c, err := New(Config{Endpoint: url + "/", AppID: "app", AppSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewRequiresConfig(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error for empty config")
	}
	if _, err := New(Config{Endpoint: "not a url", AppID: "a", AppSecret: "b"}); err == nil {
		t.Fatal("expected error for invalid endpoint")
	}
}

func TestTokenCachedAndRefreshed(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {})
	c := newTestClient(t, fs.URL)
	now := time.Unix(1_000_000, 0)
	c.tokens.now = func() time.Time { return now }
	ctx := context.Background()

	tok, err := c.Token(ctx)
	if err != nil || tok != "token-1" {
		t.Fatalf("Token() = %q, %v", tok, err)
	}
	now = now.Add(3500 * time.Second) // still more than 60s before expiry
	if tok, _ = c.Token(ctx); tok != "token-1" {
		t.Fatalf("expected cached token, got %q", tok)
	}
	now = now.Add(50 * time.Second) // within 60s of expiry
	if tok, _ = c.Token(ctx); tok != "token-2" {
		t.Fatalf("expected refreshed token, got %q", tok)
	}
	if n := fs.tokenCalls.Load(); n != 2 {
		t.Fatalf("token endpoint called %d times, want 2", n)
	}
}

func TestTokenFailureIsAuthError(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {})
	c, _ := New(Config{Endpoint: fs.URL, AppID: "app", AppSecret: "wrong"})
	_, err := c.Token(context.Background())
	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected AuthError, got %v", err)
	}
	if want := "invalid_client: client authentication failed"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}

func TestAPIErrorDecoding(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = fmt.Fprint(w, `{"code":"user.username_already_in_use","message":"This username is already in use."}`)
	})
	c := newTestClient(t, fs.URL)
	_, err := c.CreateUser(context.Background(), CreateUserInput{Username: "alice"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %v", err)
	}
	if apiErr.Status != 422 || apiErr.Code != "user.username_already_in_use" || apiErr.Message == "" {
		t.Fatalf("unexpected error fields: %+v", apiErr)
	}
	if !strings.Contains(err.Error(), "HTTP 422") || !strings.Contains(err.Error(), "user.username_already_in_use") {
		t.Fatalf("error string missing details: %s", err)
	}
}

func TestBearerHeaderAndUnauthorizedRetry(t *testing.T) {
	var calls atomic.Int32
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-2" {
			t.Errorf("Authorization = %q, want fresh token", got)
		}
		_, _ = fmt.Fprint(w, `{"id":"u1","username":"alice"}`)
	})
	c := newTestClient(t, fs.URL)
	u, err := c.GetUser(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "alice" {
		t.Fatalf("username = %q", u.Username)
	}
}

func TestRetryOn5xxOnlyForIdempotent(t *testing.T) {
	var gets, posts atomic.Int32
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if gets.Add(1) == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_, _ = fmt.Fprint(w, `{"id":"u1"}`)
			return
		}
		posts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	c := newTestClient(t, fs.URL)
	if _, err := c.GetUser(context.Background(), "u1"); err != nil {
		t.Fatalf("GET should succeed after one retry: %v", err)
	}
	if _, err := c.CreateUser(context.Background(), CreateUserInput{Username: "x"}); err == nil {
		t.Fatal("POST should fail")
	}
	if gets.Load() != 2 || posts.Load() != 1 {
		t.Fatalf("gets=%d posts=%d, want 2 and 1", gets.Load(), posts.Load())
	}
}

func TestListAllPaginates(t *testing.T) {
	const total = 230
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		if size != MaxPageSize {
			t.Errorf("page_size = %d", size)
		}
		var users []map[string]string
		for i := (page - 1) * size; i < min(page*size, total); i++ {
			users = append(users, map[string]string{"id": "u" + strconv.Itoa(i)})
		}
		w.Header().Set("Total-Number", strconv.Itoa(total))
		_ = json.NewEncoder(w).Encode(users)
	})
	c := newTestClient(t, fs.URL)
	users, err := c.ListAllUsers(context.Background(), UserSearch{})
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != total || users[229].ID != "u229" {
		t.Fatalf("got %d users", len(users))
	}

	page, n, err := c.ListUsers(context.Background(), UserSearch{}, Page{Page: 3, PageSize: 100})
	if err != nil || n != total || len(page) != 30 {
		t.Fatalf("ListUsers page 3: %d items, total %d, err %v", len(page), n, err)
	}
}

func TestResolveUserByEmail(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/api/users" || q.Get("search.primaryEmail") != "a@example.com" || q.Get("mode.primaryEmail") != "exact" {
			t.Errorf("unexpected request %s", r.URL)
		}
		w.Header().Set("Total-Number", "1")
		_, _ = fmt.Fprint(w, `[{"id":"u1","primaryEmail":"a@example.com"}]`)
	})
	c := newTestClient(t, fs.URL)
	u, err := c.ResolveUser(context.Background(), "a@example.com")
	if err != nil || u.ID != "u1" {
		t.Fatalf("ResolveUser = %v, %v", u, err)
	}
}

func TestRawJSONPreserved(t *testing.T) {
	const body = `{"id":"a1","name":"x","type":"SPA","unknownField":42}`
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, body) })
	c := newTestClient(t, fs.URL)
	app, err := c.GetApplication(context.Background(), "a1")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(app)
	if string(out) != body {
		t.Fatalf("marshal = %s, want raw %s", out, body)
	}
}
