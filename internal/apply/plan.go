package apply

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/reapermaga/logto-cli/internal/logto"
)

// API is the subset of the Management API client that apply needs.
type API interface {
	AppID() string

	ListRoles(ctx context.Context, roleType string) ([]logto.Role, error)
	CreateRole(ctx context.Context, in logto.CreateRoleInput) (*logto.Role, error)
	UpdateRole(ctx context.Context, id string, fields map[string]any) (*logto.Role, error)
	DeleteRole(ctx context.Context, id string) error
	RoleScopes(ctx context.Context, roleID string) ([]logto.Scope, error)
	AddRoleScopes(ctx context.Context, roleID string, scopeIDs []string) error
	RemoveRoleScope(ctx context.Context, roleID, scopeID string) error
	ListResources(ctx context.Context) ([]logto.Resource, error)

	ListAllUsers(ctx context.Context, s logto.UserSearch) ([]logto.User, error)
	CreateUser(ctx context.Context, in logto.CreateUserInput) (*logto.User, error)
	UpdateUser(ctx context.Context, id string, fields map[string]any) (*logto.User, error)
	SetUserSuspended(ctx context.Context, id string, suspended bool) (*logto.User, error)
	DeleteUser(ctx context.Context, id string) error
	UserRoles(ctx context.Context, id string) ([]logto.Role, error)
	AddUserRoles(ctx context.Context, id string, roleIDs []string) error
	RemoveUserRole(ctx context.Context, id, roleID string) error

	ListApplications(ctx context.Context, s logto.AppSearch) ([]logto.Application, error)
	CreateApplication(ctx context.Context, body map[string]any) (*logto.Application, error)
	UpdateApplication(ctx context.Context, id string, body map[string]any) (*logto.Application, error)
	DeleteApplication(ctx context.Context, id string) error
	ListSecrets(ctx context.Context, appID string) ([]logto.Secret, error)
	CreateSecret(ctx context.Context, appID, name string, expiresAt *time.Time) (*logto.Secret, error)
	ApplicationRoles(ctx context.Context, appID string) ([]logto.Role, error)
	AddApplicationRoles(ctx context.Context, appID string, roleIDs []string) error
	RemoveApplicationRole(ctx context.Context, appID, roleID string) error
}

// Options control planning.
type Options struct {
	// Prune deletes roles, users and apps that are missing from a section
	// present in the spec, and removes role/scope assignments that are not
	// listed. Sections absent from the spec are never pruned.
	Prune bool
}

// Action is the kind of a change.
type Action string

// Change actions.
const (
	Create Action = "create"
	Update Action = "update"
	Delete Action = "delete"
)

// Change is one planned operation.
type Change struct {
	Action  Action
	Kind    string // role, user, app
	Name    string
	Details []string
	run     func(ctx context.Context, st *state) error
}

// CreatedSecret is the credential of an app created by apply.
type CreatedSecret struct {
	App    string
	AppID  string
	Secret string
}

// Plan is the set of changes needed to reach the spec.
type Plan struct {
	Changes []Change
	roleIDs map[string]string
}

type state struct {
	roleIDs map[string]string
	secrets []CreatedSecret
}

// Empty reports whether nothing needs to change.
func (p *Plan) Empty() bool { return len(p.Changes) == 0 }

// Write prints the plan as a diff.
func (p *Plan) Write(w io.Writer) {
	if p.Empty() {
		_, _ = fmt.Fprintln(w, "No changes.")
		return
	}
	marks := map[Action]string{Create: "+", Update: "~", Delete: "-"}
	counts := map[Action]int{}
	for _, c := range p.Changes {
		counts[c.Action]++
		_, _ = fmt.Fprintf(w, "%s %s %s\n", marks[c.Action], c.Kind, c.Name)
		for _, d := range c.Details {
			_, _ = fmt.Fprintf(w, "    %s\n", d)
		}
	}
	_, _ = fmt.Fprintf(w, "\nPlan: %d to create, %d to update, %d to delete.\n", counts[Create], counts[Update], counts[Delete])
}

// Execute applies the plan in order and stops at the first error. Secrets of
// apps created before an error are still returned.
func (p *Plan) Execute(ctx context.Context, progress io.Writer) ([]CreatedSecret, error) {
	st := &state{roleIDs: map[string]string{}}
	for k, v := range p.roleIDs {
		st.roleIDs[k] = v
	}
	for _, c := range p.Changes {
		if err := c.run(ctx, st); err != nil {
			return st.secrets, fmt.Errorf("%s %s %s: %w", c.Action, c.Kind, c.Name, err)
		}
		if progress != nil {
			_, _ = fmt.Fprintf(progress, "%sd %s %s\n", strings.TrimSuffix(string(c.Action), "e"), c.Kind, c.Name)
		}
	}
	return st.secrets, nil
}

// planner accumulates changes and errors while comparing spec and server.
type planner struct {
	api     API
	opts    Options
	spec    *Spec
	errs    []error
	changes []Change
	deletes []Change

	roles     map[string]logto.Role // existing, by name
	roleTypes map[string]string     // existing and declared, by name

	resources  []logto.Resource
	scopeIndex map[string]map[string]string // indicator -> scope name -> scope ID
	scopeLabel map[string]string            // scope ID -> "indicator name"
}

// Build compares spec with the server and returns the plan.
func Build(ctx context.Context, api API, spec *Spec, opts Options) (*Plan, error) {
	p := &planner{api: api, opts: opts, spec: spec, roleTypes: map[string]string{}}

	roles, err := api.ListRoles(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	p.roles = map[string]logto.Role{}
	roleIDs := map[string]string{}
	for _, r := range roles {
		p.roles[r.Name] = r
		p.roleTypes[r.Name] = r.Type
		roleIDs[r.Name] = r.ID
	}
	for _, r := range deref(spec.Roles) {
		t, _ := roleType(r.Type)
		p.roleTypes[r.Name] = t
	}

	steps := []func(context.Context) error{p.planRoles, p.planUsers, p.planApps, p.pruneRoles}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return nil, err
		}
	}
	if len(p.errs) > 0 {
		return nil, errors.Join(p.errs...)
	}
	return &Plan{Changes: append(p.changes, p.deletes...), roleIDs: roleIDs}, nil
}

func (p *planner) errorf(format string, args ...any) {
	p.errs = append(p.errs, fmt.Errorf(format, args...))
}

func (p *planner) loadScopes(ctx context.Context) error {
	if p.scopeIndex != nil {
		return nil
	}
	resources, err := p.api.ListResources(ctx)
	if err != nil {
		return fmt.Errorf("list API resources: %w", err)
	}
	p.resources = resources
	p.scopeIndex = map[string]map[string]string{}
	p.scopeLabel = map[string]string{}
	for _, res := range resources {
		byName := map[string]string{}
		for _, s := range res.Scopes {
			byName[s.Name] = s.ID
			p.scopeLabel[s.ID] = res.Indicator + " " + s.Name
		}
		p.scopeIndex[res.Indicator] = byName
	}
	return nil
}

// resolveScopes maps scope references to IDs, recording errors.
func (p *planner) resolveScopes(role string, refs []ScopeRef) []string {
	var ids []string
	for _, ref := range refs {
		byName, ok := p.scopeIndex[ref.Resource]
		if !ok {
			p.errorf("role %q: API resource %q not found", role, ref.Resource)
			continue
		}
		for _, name := range ref.Names {
			id, ok := byName[name]
			if !ok {
				p.errorf("role %q: scope %q not found on resource %q", role, name, ref.Resource)
				continue
			}
			ids = append(ids, id)
		}
	}
	return ids
}

func (p *planner) planRoles(ctx context.Context) error {
	for _, spec := range deref(p.spec.Roles) {
		wantType, _ := roleType(spec.Type)
		var wantScopes []string
		if spec.Scopes != nil {
			if err := p.loadScopes(ctx); err != nil {
				return err
			}
			wantScopes = p.resolveScopes(spec.Name, spec.Scopes)
		}

		existing, ok := p.roles[spec.Name]
		if !ok {
			desc := spec.Description
			if desc == "" {
				desc = spec.Name // the API requires a description
			}
			details := []string{"type: " + wantType, "description: " + quote(desc)}
			for _, id := range wantScopes {
				details = append(details, "scope: +"+p.scopeLabel[id])
			}
			in := logto.CreateRoleInput{Name: spec.Name, Description: desc, Type: wantType, ScopeIDs: wantScopes}
			p.changes = append(p.changes, Change{
				Action: Create, Kind: "role", Name: spec.Name, Details: details,
				run: func(ctx context.Context, st *state) error {
					r, err := p.api.CreateRole(ctx, in)
					if err != nil {
						return err
					}
					st.roleIDs[r.Name] = r.ID
					return nil
				},
			})
			continue
		}

		if existing.Type != wantType {
			p.errorf("role %q is %s in Logto but %s in the spec; the type cannot be changed", spec.Name, existing.Type, wantType)
			continue
		}
		var details []string
		fields := map[string]any{}
		if spec.Description != "" && spec.Description != existing.Description {
			fields["description"] = spec.Description
			details = append(details, fmt.Sprintf("description: %s -> %s", quote(existing.Description), quote(spec.Description)))
		}
		var addScopes, removeScopes []string
		if spec.Scopes != nil {
			current, err := p.api.RoleScopes(ctx, existing.ID)
			if err != nil {
				return fmt.Errorf("list scopes of role %q: %w", spec.Name, err)
			}
			currentIDs := make([]string, 0, len(current))
			for _, s := range current {
				currentIDs = append(currentIDs, s.ID)
				if _, ok := p.scopeLabel[s.ID]; !ok {
					p.scopeLabel[s.ID] = s.Name
				}
			}
			addScopes = missing(wantScopes, currentIDs)
			if p.opts.Prune {
				removeScopes = missing(currentIDs, wantScopes)
			}
			for _, id := range addScopes {
				details = append(details, "scope: +"+p.scopeLabel[id])
			}
			for _, id := range removeScopes {
				details = append(details, "scope: -"+p.scopeLabel[id])
			}
		}
		if len(details) == 0 {
			continue
		}
		id := existing.ID
		p.changes = append(p.changes, Change{
			Action: Update, Kind: "role", Name: spec.Name, Details: details,
			run: func(ctx context.Context, _ *state) error {
				if len(fields) > 0 {
					if _, err := p.api.UpdateRole(ctx, id, fields); err != nil {
						return err
					}
				}
				if len(addScopes) > 0 {
					if err := p.api.AddRoleScopes(ctx, id, addScopes); err != nil {
						return err
					}
				}
				for _, s := range removeScopes {
					if err := p.api.RemoveRoleScope(ctx, id, s); err != nil {
						return err
					}
				}
				return nil
			},
		})
	}
	return nil
}

// checkRoleRefs verifies that roles exist (in Logto or the spec) with the
// expected type.
func (p *planner) checkRoleRefs(owner string, roles []string, wantType string) {
	for _, name := range roles {
		t, ok := p.roleTypes[name]
		switch {
		case !ok:
			p.errorf("%s: role %q is neither in Logto nor in the spec", owner, name)
		case t != wantType:
			p.errorf("%s: role %q is a %s role, expected %s", owner, name, t, wantType)
		}
	}
}

func roleIDsByName(st *state, names []string) ([]string, error) {
	ids := make([]string, 0, len(names))
	for _, n := range names {
		id, ok := st.roleIDs[n]
		if !ok {
			return nil, fmt.Errorf("role %q not found", n)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (p *planner) planUsers(ctx context.Context) error {
	if p.spec.Users == nil {
		return nil
	}
	users, err := p.api.ListAllUsers(ctx, logto.UserSearch{})
	if err != nil {
		return fmt.Errorf("list users: %w", err)
	}
	byUsername, byEmail := map[string]logto.User{}, map[string]logto.User{}
	for _, u := range users {
		if u.Username != "" {
			byUsername[u.Username] = u
		}
		if u.PrimaryEmail != "" {
			byEmail[strings.ToLower(u.PrimaryEmail)] = u
		}
	}
	matched := map[string]bool{}

	for _, spec := range *p.spec.Users {
		owner := "user " + quote(spec.key())
		p.checkRoleRefs(owner, spec.Roles, logto.RoleTypeUser)

		var existing *logto.User
		if spec.Username != "" {
			if u, ok := byUsername[spec.Username]; ok {
				existing = &u
			}
		} else if u, ok := byEmail[strings.ToLower(spec.Email)]; ok {
			existing = &u
		}

		if existing == nil {
			p.changes = append(p.changes, p.createUser(spec))
			continue
		}
		matched[existing.ID] = true
		if c, ok, err := p.updateUser(ctx, spec, *existing); err != nil {
			return err
		} else if ok {
			p.changes = append(p.changes, c)
		}
	}

	if p.opts.Prune {
		for _, u := range users {
			if matched[u.ID] {
				continue
			}
			id := u.ID
			p.deletes = append(p.deletes, Change{
				Action: Delete, Kind: "user", Name: firstNonEmpty(u.Username, u.PrimaryEmail, u.PrimaryPhone, u.ID),
				Details: []string{"id: " + u.ID},
				run:     func(ctx context.Context, _ *state) error { return p.api.DeleteUser(ctx, id) },
			})
		}
	}
	return nil
}

func (p *planner) createUser(spec UserSpec) Change {
	in := logto.CreateUserInput{
		Username:     spec.Username,
		PrimaryEmail: spec.Email,
		PrimaryPhone: spec.Phone,
		Name:         spec.Name,
		Password:     spec.Password,
	}
	var details []string
	for _, f := range [][2]string{{"username", spec.Username}, {"email", spec.Email}, {"phone", spec.Phone}, {"name", spec.Name}} {
		if f[1] != "" {
			details = append(details, f[0]+": "+quote(f[1]))
		}
	}
	if spec.Password != "" {
		details = append(details, "password: (set)")
	}
	suspended := spec.Suspended != nil && *spec.Suspended
	if suspended {
		details = append(details, "suspended: true")
	}
	for _, r := range spec.Roles {
		details = append(details, "role: +"+r)
	}
	return Change{
		Action: Create, Kind: "user", Name: spec.key(), Details: details,
		run: func(ctx context.Context, st *state) error {
			u, err := p.api.CreateUser(ctx, in)
			if err != nil {
				return err
			}
			if suspended {
				if _, err := p.api.SetUserSuspended(ctx, u.ID, true); err != nil {
					return err
				}
			}
			if len(spec.Roles) > 0 {
				ids, err := roleIDsByName(st, spec.Roles)
				if err != nil {
					return err
				}
				return p.api.AddUserRoles(ctx, u.ID, ids)
			}
			return nil
		},
	}
}

func (p *planner) updateUser(ctx context.Context, spec UserSpec, u logto.User) (Change, bool, error) {
	var details []string
	fields := map[string]any{}
	for _, f := range []struct{ label, field, want, have string }{
		{"username", "username", spec.Username, u.Username},
		{"email", "primaryEmail", spec.Email, u.PrimaryEmail},
		{"phone", "primaryPhone", spec.Phone, u.PrimaryPhone},
		{"name", "name", spec.Name, u.Name},
	} {
		if f.want != "" && f.want != f.have {
			fields[f.field] = f.want
			details = append(details, fmt.Sprintf("%s: %s -> %s", f.label, quote(f.have), quote(f.want)))
		}
	}
	setSuspended := spec.Suspended != nil && *spec.Suspended != u.IsSuspended
	if setSuspended {
		details = append(details, fmt.Sprintf("suspended: %t -> %t", u.IsSuspended, *spec.Suspended))
	}

	var addRoles []string
	var removeRoles []logto.Role
	if spec.Roles != nil {
		current, err := p.api.UserRoles(ctx, u.ID)
		if err != nil {
			return Change{}, false, fmt.Errorf("list roles of user %q: %w", spec.key(), err)
		}
		names := roleNames(current)
		addRoles = missing(spec.Roles, names)
		if p.opts.Prune {
			for _, r := range current {
				if !slices.Contains(spec.Roles, r.Name) {
					removeRoles = append(removeRoles, r)
				}
			}
		}
		for _, r := range addRoles {
			details = append(details, "role: +"+r)
		}
		for _, r := range removeRoles {
			details = append(details, "role: -"+r.Name)
		}
	}
	if len(details) == 0 {
		return Change{}, false, nil
	}
	id := u.ID
	return Change{
		Action: Update, Kind: "user", Name: spec.key(), Details: details,
		run: func(ctx context.Context, st *state) error {
			if len(fields) > 0 {
				if _, err := p.api.UpdateUser(ctx, id, fields); err != nil {
					return err
				}
			}
			if setSuspended {
				if _, err := p.api.SetUserSuspended(ctx, id, *spec.Suspended); err != nil {
					return err
				}
			}
			if len(addRoles) > 0 {
				ids, err := roleIDsByName(st, addRoles)
				if err != nil {
					return err
				}
				if err := p.api.AddUserRoles(ctx, id, ids); err != nil {
					return err
				}
			}
			for _, r := range removeRoles {
				if err := p.api.RemoveUserRole(ctx, id, r.ID); err != nil {
					return err
				}
			}
			return nil
		},
	}, true, nil
}

// managedAppTypes are the app types apply can declare, and therefore prune.
var managedAppTypes = []string{logto.AppTypeTraditional, logto.AppTypeSPA, logto.AppTypeNative, logto.AppTypeMachineToMachine}

func (p *planner) planApps(ctx context.Context) error {
	if p.spec.Apps == nil {
		return nil
	}
	apps, err := p.api.ListApplications(ctx, logto.AppSearch{})
	if err != nil {
		return fmt.Errorf("list applications: %w", err)
	}
	byName := map[string][]logto.Application{}
	for _, a := range apps {
		byName[a.Name] = append(byName[a.Name], a)
	}
	declared := map[string]bool{}

	for _, spec := range *p.spec.Apps {
		declared[spec.Name] = true
		owner := "app " + quote(spec.Name)
		wantType, _ := appType(spec.Type)
		p.checkRoleRefs(owner, spec.Roles, logto.RoleTypeMachineToMachine)

		matches := byName[spec.Name]
		switch {
		case len(matches) > 1:
			p.errorf("%s: %d applications have this name in Logto; names must be unique to be managed", owner, len(matches))
		case len(matches) == 0:
			p.changes = append(p.changes, p.createApp(spec, wantType))
		case matches[0].Type != wantType:
			p.errorf("%s is %s in Logto but %s in the spec; the type cannot be changed", owner, matches[0].Type, wantType)
		default:
			c, ok, err := p.updateApp(ctx, spec, matches[0])
			if err != nil {
				return err
			}
			if ok {
				p.changes = append(p.changes, c)
			}
		}
	}

	if p.opts.Prune {
		for _, a := range apps {
			if declared[a.Name] || a.ID == p.api.AppID() || !slices.Contains(managedAppTypes, a.Type) {
				continue
			}
			id := a.ID
			p.deletes = append(p.deletes, Change{
				Action: Delete, Kind: "app", Name: a.Name, Details: []string{"id: " + a.ID, "type: " + a.Type},
				run: func(ctx context.Context, _ *state) error { return p.api.DeleteApplication(ctx, id) },
			})
		}
	}
	return nil
}

func (p *planner) createApp(spec AppSpec, wantType string) Change {
	body := map[string]any{
		"name": spec.Name,
		"type": wantType,
		"oidcClientMetadata": map[string]any{
			"redirectUris":           nonNil(spec.RedirectURIs),
			"postLogoutRedirectUris": nonNil(spec.PostLogoutRedirectURIs),
		},
	}
	details := []string{"type: " + wantType}
	if spec.Description != "" {
		body["description"] = spec.Description
		details = append(details, "description: "+quote(spec.Description))
	}
	if spec.CORSOrigins != nil {
		body["customClientMetadata"] = map[string]any{"corsAllowedOrigins": spec.CORSOrigins}
	}
	for _, l := range []struct {
		label string
		v     []string
	}{{"redirectUris", spec.RedirectURIs}, {"postLogoutRedirectUris", spec.PostLogoutRedirectURIs}, {"corsOrigins", spec.CORSOrigins}} {
		if len(l.v) > 0 {
			details = append(details, l.label+": "+strings.Join(l.v, ", "))
		}
	}
	for _, r := range spec.Roles {
		details = append(details, "role: +"+r)
	}
	confidential := wantType == logto.AppTypeTraditional || wantType == logto.AppTypeMachineToMachine
	if confidential {
		details = append(details, "secret: (generated, printed after apply)")
	}
	return Change{
		Action: Create, Kind: "app", Name: spec.Name, Details: details,
		run: func(ctx context.Context, st *state) error {
			app, err := p.api.CreateApplication(ctx, body)
			if err != nil {
				return err
			}
			if confidential {
				secrets, err := p.api.ListSecrets(ctx, app.ID)
				if err != nil {
					return err
				}
				var value string
				if len(secrets) > 0 {
					value = secrets[0].Value
				} else {
					s, err := p.api.CreateSecret(ctx, app.ID, "default", nil)
					if err != nil {
						return err
					}
					value = s.Value
				}
				st.secrets = append(st.secrets, CreatedSecret{App: app.Name, AppID: app.ID, Secret: value})
			}
			if len(spec.Roles) > 0 {
				ids, err := roleIDsByName(st, spec.Roles)
				if err != nil {
					return err
				}
				return p.api.AddApplicationRoles(ctx, app.ID, ids)
			}
			return nil
		},
	}
}

func (p *planner) updateApp(ctx context.Context, spec AppSpec, app logto.Application) (Change, bool, error) {
	var details []string
	body := map[string]any{}
	if spec.Description != "" && spec.Description != app.Description {
		body["description"] = spec.Description
		details = append(details, fmt.Sprintf("description: %s -> %s", quote(app.Description), quote(spec.Description)))
	}

	oidc := copyMap(app.OIDCClientMetadata)
	oidcChanged := false
	for _, l := range []struct {
		key  string
		want []string
		have []string
	}{
		{"redirectUris", spec.RedirectURIs, app.RedirectURIs()},
		{"postLogoutRedirectUris", spec.PostLogoutRedirectURIs, app.PostLogoutRedirectURIs()},
	} {
		if l.want != nil && !sameSet(l.want, l.have) {
			oidc[l.key] = l.want
			oidcChanged = true
			details = append(details, fmt.Sprintf("%s: [%s] -> [%s]", l.key, strings.Join(l.have, ", "), strings.Join(l.want, ", ")))
		}
	}
	if oidcChanged {
		body["oidcClientMetadata"] = oidc
	}
	if spec.CORSOrigins != nil && !sameSet(spec.CORSOrigins, app.CORSOrigins()) {
		custom := copyMap(app.CustomClientMetadata)
		custom["corsAllowedOrigins"] = spec.CORSOrigins
		body["customClientMetadata"] = custom
		details = append(details, fmt.Sprintf("corsOrigins: [%s] -> [%s]", strings.Join(app.CORSOrigins(), ", "), strings.Join(spec.CORSOrigins, ", ")))
	}

	var addRoles []string
	var removeRoles []logto.Role
	if spec.Roles != nil && app.Type == logto.AppTypeMachineToMachine {
		current, err := p.api.ApplicationRoles(ctx, app.ID)
		if err != nil {
			return Change{}, false, fmt.Errorf("list roles of app %q: %w", spec.Name, err)
		}
		addRoles = missing(spec.Roles, roleNames(current))
		if p.opts.Prune {
			for _, r := range current {
				if !slices.Contains(spec.Roles, r.Name) {
					removeRoles = append(removeRoles, r)
				}
			}
		}
		for _, r := range addRoles {
			details = append(details, "role: +"+r)
		}
		for _, r := range removeRoles {
			details = append(details, "role: -"+r.Name)
		}
	}
	if len(details) == 0 {
		return Change{}, false, nil
	}
	id := app.ID
	return Change{
		Action: Update, Kind: "app", Name: spec.Name, Details: details,
		run: func(ctx context.Context, st *state) error {
			if len(body) > 0 {
				if _, err := p.api.UpdateApplication(ctx, id, body); err != nil {
					return err
				}
			}
			if len(addRoles) > 0 {
				ids, err := roleIDsByName(st, addRoles)
				if err != nil {
					return err
				}
				if err := p.api.AddApplicationRoles(ctx, id, ids); err != nil {
					return err
				}
			}
			for _, r := range removeRoles {
				if err := p.api.RemoveApplicationRole(ctx, id, r.ID); err != nil {
					return err
				}
			}
			return nil
		},
	}, true, nil
}

// pruneRoles deletes undeclared roles, except those held by the app this
// client authenticates as (deleting them would lock the CLI out).
func (p *planner) pruneRoles(ctx context.Context) error {
	if !p.opts.Prune || p.spec.Roles == nil {
		return nil
	}
	protected := map[string]bool{}
	held, err := p.api.ApplicationRoles(ctx, p.api.AppID())
	if err != nil && !logto.IsNotFound(err) {
		return fmt.Errorf("list roles of the CLI's own app: %w", err)
	}
	for _, r := range held {
		protected[r.ID] = true
	}
	declared := map[string]bool{}
	for _, r := range *p.spec.Roles {
		declared[r.Name] = true
	}
	names := make([]string, 0, len(p.roles))
	for name := range p.roles {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		r := p.roles[name]
		if declared[name] || protected[r.ID] {
			continue
		}
		id := r.ID
		p.deletes = append(p.deletes, Change{
			Action: Delete, Kind: "role", Name: name, Details: []string{"id: " + id, "type: " + r.Type},
			run: func(ctx context.Context, _ *state) error { return p.api.DeleteRole(ctx, id) },
		})
	}
	return nil
}

func roleNames(roles []logto.Role) []string {
	out := make([]string, 0, len(roles))
	for _, r := range roles {
		out = append(out, r.Name)
	}
	return out
}

// missing returns the items of want that are not in have.
func missing(want, have []string) []string {
	var out []string
	for _, w := range want {
		if !slices.Contains(have, w) && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}

func sameSet(a, b []string) bool {
	return len(missing(a, b)) == 0 && len(missing(b, a)) == 0
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func quote(s string) string { return fmt.Sprintf("%q", s) }

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
