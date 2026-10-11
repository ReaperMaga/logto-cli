package logto

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// UserSearch filters GET /api/users. Search is a fuzzy ("like") match over
// the default user fields.
type UserSearch struct {
	Search string
}

func (s UserSearch) query() url.Values {
	q := url.Values{}
	if s.Search != "" {
		q.Set("search", "%"+s.Search+"%")
	}
	return q
}

// ListUsers returns one page of users and the total count (-1 if unknown).
func (c *Client) ListUsers(ctx context.Context, s UserSearch, p Page) ([]User, int, error) {
	return listPage[User](ctx, c, "/api/users", s.query(), p)
}

// ListAllUsers returns every user matching s.
func (c *Client) ListAllUsers(ctx context.Context, s UserSearch) ([]User, error) {
	return listAll[User](ctx, c, "/api/users", s.query())
}

// GetUser fetches a user by ID.
func (c *Client) GetUser(ctx context.Context, id string) (*User, error) {
	var u User
	if _, err := c.do(ctx, http.MethodGet, "/api/users/"+escape(id), nil, nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// FindUsersBy returns users whose field (e.g. "username", "primaryEmail")
// exactly equals value.
func (c *Client) FindUsersBy(ctx context.Context, field, value string) ([]User, error) {
	q := url.Values{
		"search." + field: {value},
		"mode." + field:   {"exact"},
	}
	users, _, err := listPage[User](ctx, c, "/api/users", q, Page{PageSize: MaxPageSize})
	return users, err
}

// ResolveUser finds a user by ID, username or email.
func (c *Client) ResolveUser(ctx context.Context, ref string) (*User, error) {
	if strings.Contains(ref, "@") {
		return c.findOneUser(ctx, "primaryEmail", ref)
	}
	u, err := c.GetUser(ctx, ref)
	if err == nil {
		return u, nil
	}
	if !IsNotFound(err) {
		return nil, err
	}
	return c.findOneUser(ctx, "username", ref)
}

func (c *Client) findOneUser(ctx context.Context, field, value string) (*User, error) {
	users, err := c.FindUsersBy(ctx, field, value)
	if err != nil {
		return nil, err
	}
	switch len(users) {
	case 0:
		return nil, fmt.Errorf("user %q not found", value)
	case 1:
		return &users[0], nil
	default:
		return nil, fmt.Errorf("%d users match %s %q; use the user ID", len(users), field, value)
	}
}

// CreateUserInput is the body of POST /api/users. Empty fields are omitted.
type CreateUserInput struct {
	Username     string `json:"username,omitempty"`
	PrimaryEmail string `json:"primaryEmail,omitempty"`
	PrimaryPhone string `json:"primaryPhone,omitempty"`
	Name         string `json:"name,omitempty"`
	Avatar       string `json:"avatar,omitempty"`
	Password     string `json:"password,omitempty"`
}

// CreateUser creates a user.
func (c *Client) CreateUser(ctx context.Context, in CreateUserInput) (*User, error) {
	var u User
	if _, err := c.do(ctx, http.MethodPost, "/api/users", nil, in, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// UpdateUser patches basic user fields. Keys are API field names
// (username, primaryEmail, primaryPhone, name, avatar); a nil value clears it.
func (c *Client) UpdateUser(ctx context.Context, id string, fields map[string]any) (*User, error) {
	var u User
	if _, err := c.do(ctx, http.MethodPatch, "/api/users/"+escape(id), nil, fields, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// SetUserPassword sets a user's password.
func (c *Client) SetUserPassword(ctx context.Context, id, password string) error {
	_, err := c.do(ctx, http.MethodPatch, "/api/users/"+escape(id)+"/password", nil, map[string]string{"password": password}, nil)
	return err
}

// SetUserSuspended suspends or unsuspends a user.
func (c *Client) SetUserSuspended(ctx context.Context, id string, suspended bool) (*User, error) {
	var u User
	if _, err := c.do(ctx, http.MethodPatch, "/api/users/"+escape(id)+"/is-suspended", nil, map[string]bool{"isSuspended": suspended}, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// DeleteUser deletes a user.
func (c *Client) DeleteUser(ctx context.Context, id string) error {
	_, err := c.do(ctx, http.MethodDelete, "/api/users/"+escape(id), nil, nil, nil)
	return err
}

// UserRoles lists all roles assigned to a user.
func (c *Client) UserRoles(ctx context.Context, id string) ([]Role, error) {
	return listAll[Role](ctx, c, "/api/users/"+escape(id)+"/roles", nil)
}

// AddUserRoles assigns roles to a user.
func (c *Client) AddUserRoles(ctx context.Context, id string, roleIDs []string) error {
	_, err := c.do(ctx, http.MethodPost, "/api/users/"+escape(id)+"/roles", nil, map[string][]string{"roleIds": roleIDs}, nil)
	return err
}

// SetUserRoles replaces a user's roles.
func (c *Client) SetUserRoles(ctx context.Context, id string, roleIDs []string) error {
	_, err := c.do(ctx, http.MethodPut, "/api/users/"+escape(id)+"/roles", nil, map[string][]string{"roleIds": roleIDs}, nil)
	return err
}

// RemoveUserRole removes one role from a user.
func (c *Client) RemoveUserRole(ctx context.Context, id, roleID string) error {
	_, err := c.do(ctx, http.MethodDelete, "/api/users/"+escape(id)+"/roles/"+escape(roleID), nil, nil, nil)
	return err
}
