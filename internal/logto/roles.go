package logto

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// ListRoles returns all roles, optionally filtered by type ("User" or
// "MachineToMachine"; empty for all).
func (c *Client) ListRoles(ctx context.Context, roleType string) ([]Role, error) {
	q := url.Values{}
	if roleType != "" {
		q.Set("type", roleType)
	}
	return listAll[Role](ctx, c, "/api/roles", q)
}

// GetRole fetches a role by ID.
func (c *Client) GetRole(ctx context.Context, id string) (*Role, error) {
	var r Role
	if _, err := c.do(ctx, http.MethodGet, "/api/roles/"+escape(id), nil, nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ResolveRole finds a role by ID or name.
func (c *Client) ResolveRole(ctx context.Context, ref string) (*Role, error) {
	r, err := c.GetRole(ctx, ref)
	if err == nil {
		return r, nil
	}
	if !IsNotFound(err) {
		return nil, err
	}
	roles, err := c.ListRoles(ctx, "")
	if err != nil {
		return nil, err
	}
	return FindRole(roles, ref)
}

// FindRole picks a role by ID or name from an already fetched list.
func FindRole(roles []Role, ref string) (*Role, error) {
	for i := range roles {
		if roles[i].ID == ref || roles[i].Name == ref {
			return &roles[i], nil
		}
	}
	return nil, fmt.Errorf("role %q not found", ref)
}

// ResolveRoleIDs maps role names or IDs to IDs with a single role listing.
func (c *Client) ResolveRoleIDs(ctx context.Context, refs []string) ([]string, error) {
	roles, err := c.ListRoles(ctx, "")
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		r, err := FindRole(roles, ref)
		if err != nil {
			return nil, err
		}
		ids = append(ids, r.ID)
	}
	return ids, nil
}

// CreateRoleInput is the body of POST /api/roles.
type CreateRoleInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Type        string   `json:"type,omitempty"`
	ScopeIDs    []string `json:"scopeIds,omitempty"`
}

// CreateRole creates a role.
func (c *Client) CreateRole(ctx context.Context, in CreateRoleInput) (*Role, error) {
	var r Role
	if _, err := c.do(ctx, http.MethodPost, "/api/roles", nil, in, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// UpdateRole patches a role (name, description, isDefault).
func (c *Client) UpdateRole(ctx context.Context, id string, fields map[string]any) (*Role, error) {
	var r Role
	if _, err := c.do(ctx, http.MethodPatch, "/api/roles/"+escape(id), nil, fields, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// DeleteRole deletes a role.
func (c *Client) DeleteRole(ctx context.Context, id string) error {
	_, err := c.do(ctx, http.MethodDelete, "/api/roles/"+escape(id), nil, nil, nil)
	return err
}

// RoleScopes lists the scopes granted to a role.
func (c *Client) RoleScopes(ctx context.Context, roleID string) ([]Scope, error) {
	return listAll[Scope](ctx, c, "/api/roles/"+escape(roleID)+"/scopes", nil)
}

// AddRoleScopes grants scopes to a role.
func (c *Client) AddRoleScopes(ctx context.Context, roleID string, scopeIDs []string) error {
	_, err := c.do(ctx, http.MethodPost, "/api/roles/"+escape(roleID)+"/scopes", nil, map[string][]string{"scopeIds": scopeIDs}, nil)
	return err
}

// RemoveRoleScope revokes one scope from a role.
func (c *Client) RemoveRoleScope(ctx context.Context, roleID, scopeID string) error {
	_, err := c.do(ctx, http.MethodDelete, "/api/roles/"+escape(roleID)+"/scopes/"+escape(scopeID), nil, nil, nil)
	return err
}

// ListResources returns all API resources including their scopes.
func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	var resources []Resource
	q := url.Values{"includeScopes": {"true"}}
	if _, err := c.do(ctx, http.MethodGet, "/api/resources", q, nil, &resources); err != nil {
		return nil, err
	}
	return resources, nil
}
