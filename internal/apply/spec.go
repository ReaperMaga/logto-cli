// Package apply reconciles Logto users, roles and applications with a
// declarative YAML file.
package apply

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Spec is the root of a logto.yaml file. A nil section is unmanaged: it is
// neither reconciled nor pruned.
type Spec struct {
	Roles *[]RoleSpec `yaml:"roles"`
	Users *[]UserSpec `yaml:"users"`
	Apps  *[]AppSpec  `yaml:"apps"`
}

// RoleSpec declares a role, identified by name.
type RoleSpec struct {
	Name        string     `yaml:"name"`
	Description string     `yaml:"description"`
	Type        string     `yaml:"type"` // user (default) or m2m
	Scopes      []ScopeRef `yaml:"scopes"`
}

// ScopeRef names scopes of one API resource.
type ScopeRef struct {
	Resource string   `yaml:"resource"` // API resource indicator
	Names    []string `yaml:"names"`
}

// UserSpec declares a user, identified by username (or email when no
// username is given). Empty fields are left as they are in Logto.
type UserSpec struct {
	Username string `yaml:"username"`
	Email    string `yaml:"email"`
	Phone    string `yaml:"phone"`
	Name     string `yaml:"name"`
	// Password is only used when the user is created.
	Password  string   `yaml:"password"`
	Suspended *bool    `yaml:"suspended"`
	Roles     []string `yaml:"roles"`
}

// AppSpec declares an application, identified by name. Omitted lists are left
// as they are; an explicit empty list clears them.
type AppSpec struct {
	Name                   string   `yaml:"name"`
	Type                   string   `yaml:"type"` // traditional, spa, native, m2m
	Description            string   `yaml:"description"`
	RedirectURIs           []string `yaml:"redirectUris"`
	PostLogoutRedirectURIs []string `yaml:"postLogoutRedirectUris"`
	CORSOrigins            []string `yaml:"corsOrigins"`
	Roles                  []string `yaml:"roles"` // M2M roles, m2m apps only
}

func (u UserSpec) key() string {
	if u.Username != "" {
		return u.Username
	}
	return u.Email
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnv replaces ${VAR} references in a string. Unset variables are
// collected in missing; a bare $ is left alone so passwords may contain it.
func expandEnv(value string, missing *[]string) string {
	return envRef.ReplaceAllStringFunc(value, func(m string) string {
		name := envRef.FindStringSubmatch(m)[1]
		v, ok := os.LookupEnv(name)
		if !ok {
			*missing = append(*missing, name)
			return m
		}
		return v
	})
}

// expandNode expands ${VAR} in every scalar value (not in comments or keys).
func expandNode(n *yaml.Node, missing *[]string) {
	if n.Kind == yaml.ScalarNode {
		if v := expandEnv(n.Value, missing); v != n.Value {
			n.Value = v
			if n.Style == 0 {
				n.Tag = "" // let a plain value like ${SUSPENDED} resolve to bool/int
			}
		}
		return
	}
	for i, child := range n.Content {
		if n.Kind == yaml.MappingNode && i%2 == 0 {
			continue // key
		}
		expandNode(child, missing)
	}
}

// Load reads and validates a spec. ${VAR} references in values are expanded
// from the environment.
func Load(r io.Reader) (*Spec, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	if root.Kind == 0 {
		return nil, errors.New("spec is empty")
	}
	var missing []string
	expandNode(&root, &missing)
	if len(missing) > 0 {
		return nil, fmt.Errorf("environment variables not set: %s", strings.Join(missing, ", "))
	}
	// Re-encode the expanded tree so the strict decoder can reject unknown fields.
	expanded, err := yaml.Marshal(&root)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(expanded))
	dec.KnownFields(true)
	var spec Spec
	if err := dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	if err := spec.validate(); err != nil {
		return nil, err
	}
	return &spec, nil
}

// LoadFile reads a spec from a file, or from stdin when path is "-".
func LoadFile(path string, stdin io.Reader) (*Spec, error) {
	if path == "-" {
		return Load(stdin)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return Load(f)
}

func (s *Spec) validate() error {
	var errs []error
	seen := map[string]bool{}
	for i, r := range deref(s.Roles) {
		if r.Name == "" {
			errs = append(errs, fmt.Errorf("roles[%d]: name is required", i))
			continue
		}
		if seen[r.Name] {
			errs = append(errs, fmt.Errorf("roles[%d]: duplicate role %q", i, r.Name))
		}
		seen[r.Name] = true
		if _, err := roleType(r.Type); err != nil {
			errs = append(errs, fmt.Errorf("role %q: %w", r.Name, err))
		}
		for _, sc := range r.Scopes {
			if sc.Resource == "" || len(sc.Names) == 0 {
				errs = append(errs, fmt.Errorf("role %q: each scopes entry needs a resource and names", r.Name))
			}
		}
	}

	seen = map[string]bool{}
	for i, u := range deref(s.Users) {
		if u.key() == "" {
			errs = append(errs, fmt.Errorf("users[%d]: username or email is required", i))
			continue
		}
		if seen[u.key()] {
			errs = append(errs, fmt.Errorf("users[%d]: duplicate user %q", i, u.key()))
		}
		seen[u.key()] = true
	}

	seen = map[string]bool{}
	for i, a := range deref(s.Apps) {
		if a.Name == "" {
			errs = append(errs, fmt.Errorf("apps[%d]: name is required", i))
			continue
		}
		if seen[a.Name] {
			errs = append(errs, fmt.Errorf("apps[%d]: duplicate app %q", i, a.Name))
		}
		seen[a.Name] = true
		t, err := appType(a.Type)
		if err != nil {
			errs = append(errs, fmt.Errorf("app %q: %w", a.Name, err))
		} else if len(a.Roles) > 0 && t != "MachineToMachine" {
			errs = append(errs, fmt.Errorf("app %q: roles can only be set on m2m apps", a.Name))
		}
	}
	return errors.Join(errs...)
}

func deref[T any](p *[]T) []T {
	if p == nil {
		return nil
	}
	return *p
}

func roleType(s string) (string, error) {
	switch strings.ToLower(s) {
	case "", "user":
		return "User", nil
	case "m2m", "machinetomachine":
		return "MachineToMachine", nil
	}
	return "", fmt.Errorf("invalid role type %q: use user or m2m", s)
}

func appType(s string) (string, error) {
	switch strings.ToLower(s) {
	case "traditional":
		return "Traditional", nil
	case "spa":
		return "SPA", nil
	case "native":
		return "Native", nil
	case "m2m", "machinetomachine":
		return "MachineToMachine", nil
	}
	return "", fmt.Errorf("invalid app type %q: use traditional, spa, native or m2m", s)
}
