// Package logto is a small hand-written client for the Logto Management API.
//
// Paths, field names and query parameters were checked against the Logto
// v1.44.0 route sources (packages/core/src/routes).
package logto

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultResource is the Management API resource indicator of the OSS
// "default" tenant. It is fixed and unrelated to the public domain.
const DefaultResource = "https://default.logto.app/api"

const requestTimeout = 30 * time.Second

// Config holds everything needed to talk to the Management API.
type Config struct {
	Endpoint  string
	AppID     string
	AppSecret string
	Resource  string
	// HTTPClient is optional; a client with a 30s timeout is used by default.
	HTTPClient *http.Client
}

// Client talks to the Logto Management API. It is safe for concurrent use.
type Client struct {
	endpoint string
	appID    string
	secret   string
	resource string
	http     *http.Client
	tokens   tokenCache
}

// New validates cfg and returns a client.
func New(cfg Config) (*Client, error) {
	var missing []string
	if cfg.Endpoint == "" {
		missing = append(missing, "endpoint (--endpoint / LOGTO_ENDPOINT)")
	}
	if cfg.AppID == "" {
		missing = append(missing, "app ID (--app-id / LOGTO_APP_ID)")
	}
	if cfg.AppSecret == "" {
		missing = append(missing, "app secret (--app-secret / LOGTO_APP_SECRET)")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing configuration: %s", strings.Join(missing, ", "))
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid endpoint %q: expected e.g. https://auth.example.com", cfg.Endpoint)
	}
	resource := cfg.Resource
	if resource == "" {
		resource = DefaultResource
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: requestTimeout}
	}
	return &Client{
		endpoint: strings.TrimRight(cfg.Endpoint, "/"),
		appID:    cfg.AppID,
		secret:   cfg.AppSecret,
		resource: resource,
		http:     hc,
	}, nil
}

// AppID returns the ID of the M2M app the client authenticates as.
func (c *Client) AppID() string { return c.appID }

// APIError is a non-2xx response from the Management API.
type APIError struct {
	Status  int
	Code    string
	Message string
	Body    []byte
}

func (e *APIError) Error() string {
	switch {
	case e.Code != "" && e.Message != "":
		return fmt.Sprintf("logto API error (HTTP %d, %s): %s", e.Status, e.Code, e.Message)
	case e.Message != "":
		return fmt.Sprintf("logto API error (HTTP %d): %s", e.Status, e.Message)
	default:
		body := strings.TrimSpace(string(e.Body))
		if len(body) > 300 {
			body = body[:300] + "..."
		}
		if body == "" {
			body = http.StatusText(e.Status)
		}
		return fmt.Sprintf("logto API error (HTTP %d): %s", e.Status, body)
	}
}

// IsNotFound reports whether err is an API 404.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound
}

// Response is a raw API response.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Raw sends a request with an arbitrary method and path (which may include a
// query string) and returns the response even for 4xx/5xx statuses. The error
// is non-nil for transport and auth failures, and an *APIError for 4xx/5xx.
func (c *Client) Raw(ctx context.Context, method, path string, body []byte) (*Response, error) {
	return c.send(ctx, method, path, nil, body)
}

// do sends a JSON request and decodes a JSON response into out (if non-nil).
func (c *Client) do(ctx context.Context, method, path string, query url.Values, in, out any) (*Response, error) {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
	}
	resp, err := c.send(ctx, method, path, query, body)
	if err != nil {
		return resp, err
	}
	if out != nil && len(bytes.TrimSpace(resp.Body)) > 0 {
		if err := json.Unmarshal(resp.Body, out); err != nil {
			return resp, fmt.Errorf("decode response of %s %s: %w", method, path, err)
		}
	}
	return resp, nil
}

func isIdempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete:
		return true
	}
	return false
}

func (c *Client) buildURL(path string, query url.Values) (string, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u, err := url.Parse(c.endpoint + path)
	if err != nil {
		return "", fmt.Errorf("invalid path %q: %w", path, err)
	}
	if len(query) > 0 {
		q := u.Query()
		for k, vs := range query {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// send performs the request with auth. Idempotent methods are retried once on
// network errors or 5xx. Any method is retried once on 401 with a fresh token.
func (c *Client) send(ctx context.Context, method, path string, query url.Values, body []byte) (*Response, error) {
	method = strings.ToUpper(method)
	target, err := c.buildURL(path, query)
	if err != nil {
		return nil, err
	}

	retriedServer, retriedAuth := false, false
	for {
		token, err := c.Token(ctx)
		if err != nil {
			return nil, err
		}
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, target, reader)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		httpResp, err := c.http.Do(req)
		if err != nil {
			if !retriedServer && isIdempotent(method) && ctx.Err() == nil {
				retriedServer = true
				continue
			}
			return nil, fmt.Errorf("%s %s: %w", method, path, err)
		}
		respBody, err := io.ReadAll(httpResp.Body)
		_ = httpResp.Body.Close()
		if err != nil {
			if !retriedServer && isIdempotent(method) && ctx.Err() == nil {
				retriedServer = true
				continue
			}
			return nil, fmt.Errorf("%s %s: read response: %w", method, path, err)
		}
		resp := &Response{Status: httpResp.StatusCode, Header: httpResp.Header, Body: respBody}

		switch {
		case resp.Status == http.StatusUnauthorized && !retriedAuth:
			retriedAuth = true
			c.tokens.invalidate()
			continue
		case resp.Status >= 500 && !retriedServer && isIdempotent(method):
			retriedServer = true
			continue
		case resp.Status >= 400:
			return resp, decodeAPIError(resp)
		}
		return resp, nil
	}
}

func decodeAPIError(resp *Response) error {
	apiErr := &APIError{Status: resp.Status, Body: resp.Body}
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(resp.Body, &payload) == nil {
		apiErr.Code = payload.Code
		apiErr.Message = payload.Message
	}
	if resp.Status == http.StatusUnauthorized || resp.Status == http.StatusForbidden {
		return &AuthError{Err: apiErr}
	}
	return apiErr
}

// escape escapes a single path segment.
func escape(s string) string { return url.PathEscape(s) }
