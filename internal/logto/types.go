package logto

import "encoding/json"

// Application types as used by the API.
const (
	AppTypeTraditional      = "Traditional"
	AppTypeSPA              = "SPA"
	AppTypeNative           = "Native"
	AppTypeMachineToMachine = "MachineToMachine"
)

// Role types as used by the API.
const (
	RoleTypeUser             = "User"
	RoleTypeMachineToMachine = "MachineToMachine"
)

// The typed structs below keep the raw JSON they were decoded from, so that
// `--output json` prints exactly what the API returned (including fields this
// client does not model).

// User is a Logto user.
type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	PrimaryEmail string `json:"primaryEmail"`
	PrimaryPhone string `json:"primaryPhone"`
	Name         string `json:"name"`
	Avatar       string `json:"avatar"`
	IsSuspended  bool   `json:"isSuspended"`
	LastSignInAt int64  `json:"lastSignInAt"`
	CreatedAt    int64  `json:"createdAt"`

	raw json.RawMessage
}

// Application is a Logto application.
type Application struct {
	ID                   string         `json:"id"`
	Name                 string         `json:"name"`
	Description          string         `json:"description"`
	Type                 string         `json:"type"`
	OIDCClientMetadata   map[string]any `json:"oidcClientMetadata"`
	CustomClientMetadata map[string]any `json:"customClientMetadata"`
	IsThirdParty         bool           `json:"isThirdParty"`
	CreatedAt            int64          `json:"createdAt"`

	raw json.RawMessage
}

// IsConfidential reports whether the app type carries client secrets.
func (a *Application) IsConfidential() bool {
	return a.Type == AppTypeTraditional || a.Type == AppTypeMachineToMachine
}

// RedirectURIs returns oidcClientMetadata.redirectUris.
func (a *Application) RedirectURIs() []string {
	return stringList(a.OIDCClientMetadata["redirectUris"])
}

// PostLogoutRedirectURIs returns oidcClientMetadata.postLogoutRedirectUris.
func (a *Application) PostLogoutRedirectURIs() []string {
	return stringList(a.OIDCClientMetadata["postLogoutRedirectUris"])
}

// CORSOrigins returns customClientMetadata.corsAllowedOrigins.
func (a *Application) CORSOrigins() []string {
	return stringList(a.CustomClientMetadata["corsAllowedOrigins"])
}

// Role is a Logto role.
type Role struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	Type              string `json:"type"`
	IsDefault         bool   `json:"isDefault"`
	UsersCount        *int   `json:"usersCount,omitempty"`
	ApplicationsCount *int   `json:"applicationsCount,omitempty"`

	raw json.RawMessage
}

// Secret is an application secret.
type Secret struct {
	ApplicationID string `json:"applicationId"`
	Name          string `json:"name"`
	Value         string `json:"value"`
	CreatedAt     int64  `json:"createdAt"`
	ExpiresAt     *int64 `json:"expiresAt"`

	raw json.RawMessage
}

// Scope is an API resource scope (permission).
type Scope struct {
	ID          string `json:"id"`
	ResourceID  string `json:"resourceId"`
	Name        string `json:"name"`
	Description string `json:"description"`

	raw json.RawMessage
}

// Resource is an API resource, optionally with its scopes.
type Resource struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Indicator string  `json:"indicator"`
	Scopes    []Scope `json:"scopes"`

	raw json.RawMessage
}

func stringList(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Raw-preserving JSON (un)marshalling. Each type decodes through an alias to
// avoid recursion and keeps a copy of the input bytes.

func (u *User) UnmarshalJSON(b []byte) error {
	type alias User
	if err := json.Unmarshal(b, (*alias)(u)); err != nil {
		return err
	}
	u.raw = append(json.RawMessage(nil), b...)
	return nil
}

func (u User) MarshalJSON() ([]byte, error) {
	if u.raw != nil {
		return u.raw, nil
	}
	type alias User
	return json.Marshal(alias(u))
}

func (a *Application) UnmarshalJSON(b []byte) error {
	type alias Application
	if err := json.Unmarshal(b, (*alias)(a)); err != nil {
		return err
	}
	a.raw = append(json.RawMessage(nil), b...)
	return nil
}

func (a Application) MarshalJSON() ([]byte, error) {
	if a.raw != nil {
		return a.raw, nil
	}
	type alias Application
	return json.Marshal(alias(a))
}

func (r *Role) UnmarshalJSON(b []byte) error {
	type alias Role
	if err := json.Unmarshal(b, (*alias)(r)); err != nil {
		return err
	}
	r.raw = append(json.RawMessage(nil), b...)
	return nil
}

func (r Role) MarshalJSON() ([]byte, error) {
	if r.raw != nil {
		return r.raw, nil
	}
	type alias Role
	return json.Marshal(alias(r))
}

func (s *Secret) UnmarshalJSON(b []byte) error {
	type alias Secret
	if err := json.Unmarshal(b, (*alias)(s)); err != nil {
		return err
	}
	s.raw = append(json.RawMessage(nil), b...)
	return nil
}

func (s Secret) MarshalJSON() ([]byte, error) {
	if s.raw != nil {
		return s.raw, nil
	}
	type alias Secret
	return json.Marshal(alias(s))
}

func (s *Scope) UnmarshalJSON(b []byte) error {
	type alias Scope
	if err := json.Unmarshal(b, (*alias)(s)); err != nil {
		return err
	}
	s.raw = append(json.RawMessage(nil), b...)
	return nil
}

func (s Scope) MarshalJSON() ([]byte, error) {
	if s.raw != nil {
		return s.raw, nil
	}
	type alias Scope
	return json.Marshal(alias(s))
}

func (r *Resource) UnmarshalJSON(b []byte) error {
	type alias Resource
	if err := json.Unmarshal(b, (*alias)(r)); err != nil {
		return err
	}
	r.raw = append(json.RawMessage(nil), b...)
	return nil
}

func (r Resource) MarshalJSON() ([]byte, error) {
	if r.raw != nil {
		return r.raw, nil
	}
	type alias Resource
	return json.Marshal(alias(r))
}
