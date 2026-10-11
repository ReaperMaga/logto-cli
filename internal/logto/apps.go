package logto

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// AppSearch filters GET /api/applications.
type AppSearch struct {
	Types  []string // API enum values, e.g. "Traditional"
	Search string   // fuzzy match
}

func (s AppSearch) query() url.Values {
	q := url.Values{}
	for _, t := range s.Types {
		q.Add("types", t)
	}
	if s.Search != "" {
		q.Set("search", "%"+s.Search+"%")
	}
	return q
}

// ListApplications returns all applications matching s. The endpoint's
// pagination is optional; without page params it returns everything.
func (c *Client) ListApplications(ctx context.Context, s AppSearch) ([]Application, error) {
	var apps []Application
	if _, err := c.do(ctx, http.MethodGet, "/api/applications", s.query(), nil, &apps); err != nil {
		return nil, err
	}
	return apps, nil
}

// GetApplication fetches an application by ID.
func (c *Client) GetApplication(ctx context.Context, id string) (*Application, error) {
	var a Application
	if _, err := c.do(ctx, http.MethodGet, "/api/applications/"+escape(id), nil, nil, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// ResolveApplication finds an application by ID or exact name.
func (c *Client) ResolveApplication(ctx context.Context, ref string) (*Application, error) {
	a, err := c.GetApplication(ctx, ref)
	if err == nil {
		return a, nil
	}
	if !IsNotFound(err) {
		return nil, err
	}
	apps, err := c.ListApplications(ctx, AppSearch{Search: ref})
	if err != nil {
		return nil, err
	}
	var matches []Application
	for _, app := range apps {
		if app.Name == ref {
			matches = append(matches, app)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("application %q not found", ref)
	case 1:
		return &matches[0], nil
	default:
		return nil, fmt.Errorf("%d applications are named %q; use the application ID", len(matches), ref)
	}
}

// CreateApplication creates an application. body follows the API schema
// (name, type, description, oidcClientMetadata, customClientMetadata).
func (c *Client) CreateApplication(ctx context.Context, body map[string]any) (*Application, error) {
	var a Application
	if _, err := c.do(ctx, http.MethodPost, "/api/applications", nil, body, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// UpdateApplication patches an application.
func (c *Client) UpdateApplication(ctx context.Context, id string, body map[string]any) (*Application, error) {
	var a Application
	if _, err := c.do(ctx, http.MethodPatch, "/api/applications/"+escape(id), nil, body, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// DeleteApplication deletes an application.
func (c *Client) DeleteApplication(ctx context.Context, id string) error {
	_, err := c.do(ctx, http.MethodDelete, "/api/applications/"+escape(id), nil, nil, nil)
	return err
}

// ListSecrets lists an application's secrets.
func (c *Client) ListSecrets(ctx context.Context, appID string) ([]Secret, error) {
	var secrets []Secret
	if _, err := c.do(ctx, http.MethodGet, "/api/applications/"+escape(appID)+"/secrets", nil, nil, &secrets); err != nil {
		return nil, err
	}
	return secrets, nil
}

// CreateSecret creates a named application secret. expiresAt may be nil.
func (c *Client) CreateSecret(ctx context.Context, appID, name string, expiresAt *time.Time) (*Secret, error) {
	body := map[string]any{"name": name}
	if expiresAt != nil {
		body["expiresAt"] = expiresAt.UnixMilli()
	}
	var s Secret
	if _, err := c.do(ctx, http.MethodPost, "/api/applications/"+escape(appID)+"/secrets", nil, body, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// DeleteSecret deletes a named application secret.
func (c *Client) DeleteSecret(ctx context.Context, appID, name string) error {
	_, err := c.do(ctx, http.MethodDelete, "/api/applications/"+escape(appID)+"/secrets/"+escape(name), nil, nil, nil)
	return err
}

// ApplicationRoles lists the roles assigned to an M2M application.
func (c *Client) ApplicationRoles(ctx context.Context, appID string) ([]Role, error) {
	return listAll[Role](ctx, c, "/api/applications/"+escape(appID)+"/roles", nil)
}

// AddApplicationRoles assigns M2M roles to an M2M application.
func (c *Client) AddApplicationRoles(ctx context.Context, appID string, roleIDs []string) error {
	_, err := c.do(ctx, http.MethodPost, "/api/applications/"+escape(appID)+"/roles", nil, map[string][]string{"roleIds": roleIDs}, nil)
	return err
}

// RemoveApplicationRole removes one role from an M2M application.
func (c *Client) RemoveApplicationRole(ctx context.Context, appID, roleID string) error {
	_, err := c.do(ctx, http.MethodDelete, "/api/applications/"+escape(appID)+"/roles/"+escape(roleID), nil, nil, nil)
	return err
}
