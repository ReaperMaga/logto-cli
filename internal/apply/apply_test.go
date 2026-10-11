package apply

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/reapermaga/logto-cli/internal/logto"
)

// fakeAPI is an in-memory Management API.
type fakeAPI struct {
	selfID    string
	nextID    int
	roles     map[string]*logto.Role
	roleScope map[string][]string
	users     map[string]*logto.User
	userRoles map[string][]string
	apps      map[string]*logto.Application
	appRoles  map[string][]string
	secrets   map[string][]logto.Secret
	resources []logto.Resource
	calls     []string
}

func newFake() *fakeAPI {
	f := &fakeAPI{
		selfID:    "self",
		roles:     map[string]*logto.Role{},
		roleScope: map[string][]string{},
		users:     map[string]*logto.User{},
		userRoles: map[string][]string{},
		apps:      map[string]*logto.Application{},
		appRoles:  map[string][]string{},
		secrets:   map[string][]logto.Secret{},
		resources: []logto.Resource{{
			ID: "res1", Indicator: "https://api.example.com",
			Scopes: []logto.Scope{{ID: "s-read", ResourceID: "res1", Name: "read"}, {ID: "s-write", ResourceID: "res1", Name: "write"}},
		}},
	}
	// The CLI's own app and the seeded management role it holds.
	f.apps["self"] = &logto.Application{ID: "self", Name: "nomad-bootstrap", Type: logto.AppTypeMachineToMachine}
	f.roles["mgmt"] = &logto.Role{ID: "mgmt", Name: "Logto Management API access", Type: logto.RoleTypeMachineToMachine}
	f.appRoles["self"] = []string{"mgmt"}
	return f
}

func (f *fakeAPI) id(prefix string) string {
	f.nextID++
	return fmt.Sprintf("%s%d", prefix, f.nextID)
}

func (f *fakeAPI) record(format string, args ...any) {
	f.calls = append(f.calls, fmt.Sprintf(format, args...))
}

func (f *fakeAPI) AppID() string { return f.selfID }

func (f *fakeAPI) ListRoles(_ context.Context, t string) ([]logto.Role, error) {
	var out []logto.Role
	for _, r := range f.roles {
		if t == "" || r.Type == t {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeAPI) CreateRole(_ context.Context, in logto.CreateRoleInput) (*logto.Role, error) {
	r := &logto.Role{ID: f.id("r"), Name: in.Name, Description: in.Description, Type: in.Type}
	f.roles[r.ID] = r
	f.roleScope[r.ID] = append([]string(nil), in.ScopeIDs...)
	f.record("CreateRole %s", in.Name)
	return r, nil
}

func (f *fakeAPI) UpdateRole(_ context.Context, id string, fields map[string]any) (*logto.Role, error) {
	if d, ok := fields["description"].(string); ok {
		f.roles[id].Description = d
	}
	f.record("UpdateRole %s", id)
	return f.roles[id], nil
}

func (f *fakeAPI) DeleteRole(_ context.Context, id string) error {
	delete(f.roles, id)
	f.record("DeleteRole %s", id)
	return nil
}

func (f *fakeAPI) RoleScopes(_ context.Context, id string) ([]logto.Scope, error) {
	var out []logto.Scope
	for _, sid := range f.roleScope[id] {
		for _, s := range f.resources[0].Scopes {
			if s.ID == sid {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

func (f *fakeAPI) AddRoleScopes(_ context.Context, id string, scopeIDs []string) error {
	f.roleScope[id] = append(f.roleScope[id], scopeIDs...)
	f.record("AddRoleScopes %s %v", id, scopeIDs)
	return nil
}

func (f *fakeAPI) RemoveRoleScope(_ context.Context, id, scopeID string) error {
	f.roleScope[id] = slices.DeleteFunc(f.roleScope[id], func(s string) bool { return s == scopeID })
	f.record("RemoveRoleScope %s %s", id, scopeID)
	return nil
}

func (f *fakeAPI) ListResources(context.Context) ([]logto.Resource, error) { return f.resources, nil }

func (f *fakeAPI) ListAllUsers(context.Context, logto.UserSearch) ([]logto.User, error) {
	var out []logto.User
	for _, u := range f.users {
		out = append(out, *u)
	}
	return out, nil
}

func (f *fakeAPI) CreateUser(_ context.Context, in logto.CreateUserInput) (*logto.User, error) {
	u := &logto.User{ID: f.id("u"), Username: in.Username, PrimaryEmail: in.PrimaryEmail, PrimaryPhone: in.PrimaryPhone, Name: in.Name}
	f.users[u.ID] = u
	f.record("CreateUser %s", in.Username)
	return u, nil
}

func (f *fakeAPI) UpdateUser(_ context.Context, id string, fields map[string]any) (*logto.User, error) {
	u := f.users[id]
	if v, ok := fields["primaryEmail"].(string); ok {
		u.PrimaryEmail = v
	}
	if v, ok := fields["name"].(string); ok {
		u.Name = v
	}
	f.record("UpdateUser %s", id)
	return u, nil
}

func (f *fakeAPI) SetUserSuspended(_ context.Context, id string, s bool) (*logto.User, error) {
	f.users[id].IsSuspended = s
	f.record("SetUserSuspended %s %t", id, s)
	return f.users[id], nil
}

func (f *fakeAPI) DeleteUser(_ context.Context, id string) error {
	delete(f.users, id)
	f.record("DeleteUser %s", id)
	return nil
}

func (f *fakeAPI) rolesOf(ids []string) []logto.Role {
	var out []logto.Role
	for _, id := range ids {
		out = append(out, *f.roles[id])
	}
	return out
}

func (f *fakeAPI) UserRoles(_ context.Context, id string) ([]logto.Role, error) {
	return f.rolesOf(f.userRoles[id]), nil
}

func (f *fakeAPI) AddUserRoles(_ context.Context, id string, roleIDs []string) error {
	f.userRoles[id] = append(f.userRoles[id], roleIDs...)
	f.record("AddUserRoles %s %v", id, roleIDs)
	return nil
}

func (f *fakeAPI) RemoveUserRole(_ context.Context, id, roleID string) error {
	f.userRoles[id] = slices.DeleteFunc(f.userRoles[id], func(r string) bool { return r == roleID })
	f.record("RemoveUserRole %s %s", id, roleID)
	return nil
}

func (f *fakeAPI) ListApplications(context.Context, logto.AppSearch) ([]logto.Application, error) {
	var out []logto.Application
	for _, a := range f.apps {
		out = append(out, *a)
	}
	return out, nil
}

func (f *fakeAPI) CreateApplication(_ context.Context, body map[string]any) (*logto.Application, error) {
	a := &logto.Application{ID: f.id("a"), Name: body["name"].(string), Type: body["type"].(string)}
	if d, ok := body["description"].(string); ok {
		a.Description = d
	}
	a.OIDCClientMetadata = toAnyMap(body["oidcClientMetadata"])
	a.CustomClientMetadata = toAnyMap(body["customClientMetadata"])
	f.apps[a.ID] = a
	if a.IsConfidential() {
		f.secrets[a.ID] = []logto.Secret{{Name: "default", Value: "secret-" + a.ID}}
	}
	f.record("CreateApplication %s", a.Name)
	return a, nil
}

// toAnyMap converts []string values to []any, as JSON decoding would.
func toAnyMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	out := map[string]any{}
	for k, val := range m {
		if ss, ok := val.([]string); ok {
			items := make([]any, len(ss))
			for i, s := range ss {
				items[i] = s
			}
			out[k] = items
		} else {
			out[k] = val
		}
	}
	return out
}

func (f *fakeAPI) UpdateApplication(_ context.Context, id string, body map[string]any) (*logto.Application, error) {
	a := f.apps[id]
	if m, ok := body["oidcClientMetadata"]; ok {
		a.OIDCClientMetadata = toAnyMap(m)
	}
	if m, ok := body["customClientMetadata"]; ok {
		a.CustomClientMetadata = toAnyMap(m)
	}
	f.record("UpdateApplication %s", id)
	return a, nil
}

func (f *fakeAPI) DeleteApplication(_ context.Context, id string) error {
	delete(f.apps, id)
	f.record("DeleteApplication %s", id)
	return nil
}

func (f *fakeAPI) ListSecrets(_ context.Context, id string) ([]logto.Secret, error) {
	return f.secrets[id], nil
}

func (f *fakeAPI) CreateSecret(_ context.Context, id, name string, _ *time.Time) (*logto.Secret, error) {
	s := logto.Secret{Name: name, Value: "new"}
	f.secrets[id] = append(f.secrets[id], s)
	return &s, nil
}

func (f *fakeAPI) ApplicationRoles(_ context.Context, id string) ([]logto.Role, error) {
	return f.rolesOf(f.appRoles[id]), nil
}

func (f *fakeAPI) AddApplicationRoles(_ context.Context, id string, roleIDs []string) error {
	f.appRoles[id] = append(f.appRoles[id], roleIDs...)
	f.record("AddApplicationRoles %s %v", id, roleIDs)
	return nil
}

func (f *fakeAPI) RemoveApplicationRole(_ context.Context, id, roleID string) error {
	f.appRoles[id] = slices.DeleteFunc(f.appRoles[id], func(r string) bool { return r == roleID })
	return nil
}

const sampleSpec = `
roles:
  - name: admin
    description: Administrators
    scopes:
      - resource: https://api.example.com
        names: [read, write]
  - name: ci
    type: m2m
users:
  - username: alice
    email: alice@example.com
    name: Alice
    password: ${TEST_ALICE_PASSWORD}
    roles: [admin]
  - username: bob
    suspended: true
apps:
  - name: grafana
    type: traditional
    redirectUris: [https://grafana.example.com/login/generic_oauth]
    corsOrigins: [https://grafana.example.com]
  - name: deployer
    type: m2m
    roles: [ci]
  - name: web
    type: spa
`

func mustLoad(t *testing.T, s string) *Spec {
	t.Helper()
	spec, err := Load(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestApplyIsIdempotent(t *testing.T) {
	t.Setenv("TEST_ALICE_PASSWORD", "pa$$word")
	ctx := context.Background()
	api := newFake()
	spec := mustLoad(t, sampleSpec)

	plan, err := Build(ctx, api, spec, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	plan.Write(&out)
	if !strings.Contains(out.String(), "Plan: 7 to create, 0 to update, 0 to delete.") {
		t.Fatalf("unexpected plan:\n%s", out.String())
	}

	secrets, err := plan.Execute(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 2 {
		t.Fatalf("expected secrets for grafana and deployer, got %+v", secrets)
	}
	if got := len(api.users); got != 2 {
		t.Fatalf("%d users", got)
	}

	// Second run: nothing to do.
	plan, err = Build(ctx, api, spec, Options{Prune: true})
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	plan.Write(&out)
	if !plan.Empty() {
		t.Fatalf("second apply should be a no-op, got:\n%s", out.String())
	}
	if out.String() != "No changes.\n" {
		t.Fatalf("output = %q", out.String())
	}
}

func TestApplyUpdatesAndPrunes(t *testing.T) {
	t.Setenv("TEST_ALICE_PASSWORD", "x")
	ctx := context.Background()
	api := newFake()
	plan, err := Build(ctx, api, mustLoad(t, sampleSpec), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Execute(ctx, nil); err != nil {
		t.Fatal(err)
	}

	changed := `
roles:
  - name: admin
    scopes:
      - resource: https://api.example.com
        names: [read]
users:
  - username: alice
    email: alice@new.example.com
    roles: []
apps:
  - name: grafana
    type: traditional
    redirectUris: [https://grafana.example.com/cb]
`
	plan, err = Build(ctx, api, mustLoad(t, changed), Options{Prune: true})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	plan.Write(&out)
	text := out.String()
	for _, want := range []string{
		"~ role admin", "scope: -https://api.example.com write",
		"~ user alice", `email: "alice@example.com" -> "alice@new.example.com"`, "role: -admin",
		"~ app grafana", "- app deployer", "- app web", "- user bob", "- role ci",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("plan missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "nomad-bootstrap") || strings.Contains(text, "Management API") {
		t.Errorf("plan must not touch the CLI's own app or its role:\n%s", text)
	}
	if _, err := plan.Execute(ctx, nil); err != nil {
		t.Fatal(err)
	}

	plan, err = Build(ctx, api, mustLoad(t, changed), Options{Prune: true})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Empty() {
		out.Reset()
		plan.Write(&out)
		t.Fatalf("expected no changes after applying, got:\n%s", out.String())
	}
}

func TestPlanErrors(t *testing.T) {
	ctx := context.Background()
	api := newFake()
	spec := mustLoad(t, `
users:
  - username: carol
    roles: [missing-role]
apps:
  - name: svc
    type: m2m
    roles: [admin]
roles:
  - name: admin
  - name: bad
    scopes:
      - resource: https://api.example.com
        names: [nope]
`)
	_, err := Build(ctx, api, spec, Options{})
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{`role "missing-role" is neither`, `role "admin" is a User role`, `scope "nope" not found`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestLoadValidation(t *testing.T) {
	cases := map[string]string{
		"unknown field":  "users:\n  - username: a\n    colour: red\n",
		"missing env":    "users:\n  - username: a\n    password: ${LOGTO_CLI_TEST_UNSET_VAR}\n",
		"bad app type":   "apps:\n  - name: a\n    type: desktop\n",
		"roles on spa":   "apps:\n  - name: a\n    type: spa\n    roles: [x]\n",
		"duplicate user": "users:\n  - username: a\n  - username: a\n",
		"empty":          "",
	}
	for name, input := range cases {
		if _, err := Load(strings.NewReader(input)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestExampleSpecLoads(t *testing.T) {
	t.Setenv("ALICE_INITIAL_PASSWORD", "x")
	if _, err := LoadFile("../../testdata/logto.example.yaml", nil); err != nil {
		t.Fatal(err)
	}
}

func TestEnvExpansion(t *testing.T) {
	t.Setenv("TEST_PW", `p@ss: "word" #1`)
	t.Setenv("TEST_SUSPENDED", "true")
	spec := mustLoad(t, "# ${NOT_EXPANDED_IN_COMMENTS}\nusers:\n  - username: a\n    password: ${TEST_PW}\n    suspended: ${TEST_SUSPENDED}\n")
	u := (*spec.Users)[0]
	if u.Password != `p@ss: "word" #1` || u.Suspended == nil || !*u.Suspended {
		t.Fatalf("unexpected user: %+v", u)
	}
}
