package logto

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// tokenRefreshMargin is how long before expiry a cached token is refreshed.
const tokenRefreshMargin = 60 * time.Second

// AuthError means the client could not authenticate (token request failed, or
// the API answered 401/403).
type AuthError struct {
	Err error
}

func (e *AuthError) Error() string { return "authentication failed: " + e.Err.Error() }
func (e *AuthError) Unwrap() error { return e.Err }

type tokenCache struct {
	mu     sync.Mutex
	token  string
	expiry time.Time
	// now is overridable in tests.
	now func() time.Time
}

func (t *tokenCache) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

func (t *tokenCache) invalidate() {
	t.mu.Lock()
	t.token = ""
	t.mu.Unlock()
}

// Token returns a Management API access token, fetching a new one when the
// cached token is missing or expires within 60 seconds. Tokens are cached in
// memory only.
func (c *Client) Token(ctx context.Context) (string, error) {
	c.tokens.mu.Lock()
	defer c.tokens.mu.Unlock()
	if c.tokens.token != "" && c.tokens.clock().Before(c.tokens.expiry) {
		return c.tokens.token, nil
	}
	token, expiresIn, err := c.fetchToken(ctx)
	if err != nil {
		return "", err
	}
	c.tokens.token = token
	c.tokens.expiry = c.tokens.clock().Add(time.Duration(expiresIn)*time.Second - tokenRefreshMargin)
	return token, nil
}

func (c *Client) fetchToken(ctx context.Context) (string, int64, error) {
	form := url.Values{
		"grant_type": {"client_credentials"},
		"resource":   {c.resource},
		"scope":      {"all"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/oidc/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.SetBasicAuth(url.QueryEscape(c.appID), url.QueryEscape(c.secret))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", 0, &AuthError{Err: fmt.Errorf("token request: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, &AuthError{Err: fmt.Errorf("token request: read response: %w", err)}
	}

	var payload struct {
		AccessToken      string `json:"access_token"`
		ExpiresIn        int64  `json:"expires_in"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &payload)
	if resp.StatusCode != http.StatusOK || payload.AccessToken == "" {
		msg := payload.Error
		if payload.ErrorDescription != "" {
			msg += ": " + payload.ErrorDescription
		}
		if msg == "" {
			msg = strings.TrimSpace(string(body))
		}
		return "", 0, &AuthError{Err: fmt.Errorf("token endpoint returned HTTP %d: %s", resp.StatusCode, msg)}
	}
	return payload.AccessToken, payload.ExpiresIn, nil
}
