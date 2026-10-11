package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/reapermaga/logto-cli/internal/logto"
)

// AppTypes maps CLI type names to the API enum.
var appTypes = map[string]string{
	"traditional": logto.AppTypeTraditional,
	"spa":         logto.AppTypeSPA,
	"native":      logto.AppTypeNative,
	"m2m":         logto.AppTypeMachineToMachine,
}

// ParseAppType maps traditional|spa|native|m2m (or the API enum value) to the API enum.
func parseAppType(s string) (string, error) {
	if t, ok := appTypes[strings.ToLower(s)]; ok {
		return t, nil
	}
	for _, t := range appTypes {
		if t == s {
			return t, nil
		}
	}
	return "", fmt.Errorf("invalid application type %q: use traditional, spa, native or m2m", s)
}

func (a *app) appsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "apps",
		Aliases: []string{"app", "applications"},
		Short:   "Manage applications",
	}
	cmd.AddCommand(
		a.appsListCmd(),
		a.appsGetCmd(),
		a.appsCreateCmd(),
		a.appsUpdateCmd(),
		a.appsDeleteCmd(),
		a.appsSecretsCmd(),
		a.appsRolesCmd(),
	)
	return cmd
}

func (a *app) printApps(apps []logto.Application) error {
	if a.jsonOutput() {
		if apps == nil {
			apps = []logto.Application{}
		}
		return a.printJSON(apps)
	}
	rows := make([][]string, 0, len(apps))
	for _, app := range apps {
		rows = append(rows, []string{app.ID, app.Name, app.Type, strings.Join(app.RedirectURIs(), ","), fmtTime(app.CreatedAt)})
	}
	return a.printTable([]string{"ID", "NAME", "TYPE", "REDIRECT URIS", "CREATED"}, rows)
}

func (a *app) printApp(app *logto.Application) error {
	if a.jsonOutput() {
		return a.printJSON(app)
	}
	return a.printFields(
		[2]string{"ID", app.ID},
		[2]string{"Name", app.Name},
		[2]string{"Type", app.Type},
		[2]string{"Description", app.Description},
		[2]string{"Redirect URIs", strings.Join(app.RedirectURIs(), ", ")},
		[2]string{"Post-logout URIs", strings.Join(app.PostLogoutRedirectURIs(), ", ")},
		[2]string{"CORS origins", strings.Join(app.CORSOrigins(), ", ")},
		[2]string{"Created", fmtTime(app.CreatedAt)},
	)
}

func (a *app) appsListCmd() *cobra.Command {
	var (
		types  []string
		search string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List applications",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s := logto.AppSearch{Search: search}
			for _, t := range types {
				apiType, err := parseAppType(t)
				if err != nil {
					return err
				}
				s.Types = append(s.Types, apiType)
			}
			c, err := a.api()
			if err != nil {
				return err
			}
			apps, err := c.ListApplications(cmd.Context(), s)
			if err != nil {
				return err
			}
			return a.printApps(apps)
		},
	}
	cmd.Flags().StringSliceVar(&types, "type", nil, "filter by type: traditional, spa, native, m2m (repeatable)")
	cmd.Flags().StringVar(&search, "search", "", "fuzzy search")
	return cmd
}

func (a *app) appsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id|name>",
		Short: "Show an application",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.api()
			if err != nil {
				return err
			}
			app, err := c.ResolveApplication(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return a.printApp(app)
		},
	}
}

// appFlags are the flags shared by apps create and apps update.
type appFlags struct {
	name, appType, description                string
	redirectURIs, postLogoutURIs, corsOrigins []string
}

func (f *appFlags) register(cmd *cobra.Command) {
	fs := cmd.Flags()
	fs.StringVar(&f.name, "name", "", "application name")
	fs.StringVar(&f.description, "description", "", "description")
	fs.StringSliceVar(&f.redirectURIs, "redirect-uri", nil, "redirect URI (repeatable)")
	fs.StringSliceVar(&f.postLogoutURIs, "post-logout-uri", nil, "post sign-out redirect URI (repeatable)")
	fs.StringSliceVar(&f.corsOrigins, "cors-origin", nil, "allowed CORS origin (repeatable)")
}

func (a *app) appsCreateCmd() *cobra.Command {
	var f appFlags
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an application (prints client ID and secret for confidential types)",
		Example: `  logto-cli apps create --name grafana --type traditional \
    --redirect-uri https://grafana.example.com/login/generic_oauth`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			appType, err := parseAppType(f.appType)
			if err != nil {
				return err
			}
			body := map[string]any{
				"name": f.name,
				"type": appType,
				"oidcClientMetadata": map[string]any{
					"redirectUris":           nonNil(f.redirectURIs),
					"postLogoutRedirectUris": nonNil(f.postLogoutURIs),
				},
			}
			if f.description != "" {
				body["description"] = f.description
			}
			if len(f.corsOrigins) > 0 {
				body["customClientMetadata"] = map[string]any{"corsAllowedOrigins": f.corsOrigins}
			}
			c, err := a.api()
			if err != nil {
				return err
			}
			app, err := c.CreateApplication(cmd.Context(), body)
			if err != nil {
				return err
			}
			var secret *logto.Secret
			if app.IsConfidential() {
				if secret, err = ensureSecret(cmd, c, app.ID); err != nil {
					return fmt.Errorf("application %s created, but reading its secret failed: %w", app.ID, err)
				}
			}
			if a.jsonOutput() {
				return a.printJSON(map[string]any{"application": app, "secret": secret})
			}
			if err := a.printApp(app); err != nil {
				return err
			}
			if secret != nil {
				_, _ = fmt.Fprintf(a.stdout, "\nClient ID:      %s\nClient secret:  %s\n", app.ID, secret.Value)
			}
			return nil
		},
	}
	f.register(cmd)
	cmd.Flags().StringVar(&f.appType, "type", "", "traditional, spa, native or m2m")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("type")
	return cmd
}

// ensureSecret returns the app's first secret, creating one named "default"
// if the app has none.
func ensureSecret(cmd *cobra.Command, c *logto.Client, appID string) (*logto.Secret, error) {
	secrets, err := c.ListSecrets(cmd.Context(), appID)
	if err != nil {
		return nil, err
	}
	if len(secrets) > 0 {
		return &secrets[0], nil
	}
	return c.CreateSecret(cmd.Context(), appID, "default", nil)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (a *app) appsUpdateCmd() *cobra.Command {
	var f appFlags
	cmd := &cobra.Command{
		Use:   "update <id|name>",
		Short: "Update an application (list flags replace the whole list)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			c, err := a.api()
			if err != nil {
				return err
			}
			app, err := c.ResolveApplication(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			body := map[string]any{}
			if changed("name") {
				body["name"] = f.name
			}
			if changed("description") {
				body["description"] = f.description
			}
			if changed("redirect-uri") || changed("post-logout-uri") {
				meta := copyMap(app.OIDCClientMetadata)
				if changed("redirect-uri") {
					meta["redirectUris"] = nonNil(f.redirectURIs)
				}
				if changed("post-logout-uri") {
					meta["postLogoutRedirectUris"] = nonNil(f.postLogoutURIs)
				}
				body["oidcClientMetadata"] = meta
			}
			if changed("cors-origin") {
				meta := copyMap(app.CustomClientMetadata)
				meta["corsAllowedOrigins"] = nonNil(f.corsOrigins)
				body["customClientMetadata"] = meta
			}
			if len(body) == 0 {
				return errors.New("nothing to update: pass at least one of --name, --description, --redirect-uri, --post-logout-uri, --cors-origin")
			}
			app, err = c.UpdateApplication(cmd.Context(), app.ID, body)
			if err != nil {
				return err
			}
			return a.printApp(app)
		},
	}
	f.register(cmd)
	return cmd
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (a *app) appsDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id|name>",
		Short: "Delete an application",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.api()
			if err != nil {
				return err
			}
			app, err := c.ResolveApplication(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if app.ID == c.AppID() {
				a.warnf("%s is the application this CLI is authenticated as", app.ID)
			}
			if !yes {
				ok, err := a.confirm(fmt.Sprintf("Delete application %s (%s)?", app.ID, app.Name))
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("aborted")
				}
			}
			if err := c.DeleteApplication(cmd.Context(), app.ID); err != nil {
				return err
			}
			a.infof("Deleted application %s.", app.ID)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

func (a *app) appsSecretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Manage application secrets",
	}

	list := &cobra.Command{
		Use:   "list <app>",
		Short: "List an application's secrets (values included)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.api()
			if err != nil {
				return err
			}
			app, err := c.ResolveApplication(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			secrets, err := c.ListSecrets(cmd.Context(), app.ID)
			if err != nil {
				return err
			}
			if a.jsonOutput() {
				if secrets == nil {
					secrets = []logto.Secret{}
				}
				return a.printJSON(secrets)
			}
			rows := make([][]string, 0, len(secrets))
			for _, s := range secrets {
				expires := ""
				if s.ExpiresAt != nil {
					expires = fmtTime(*s.ExpiresAt)
				}
				rows = append(rows, []string{s.Name, s.Value, fmtTime(s.CreatedAt), expires})
			}
			return a.printTable([]string{"NAME", "VALUE", "CREATED", "EXPIRES"}, rows)
		},
	}

	var name, expiresAt string
	create := &cobra.Command{
		Use:   "create <app>",
		Short: "Create a named secret and print its value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var expires *time.Time
			if expiresAt != "" {
				t, err := time.Parse(time.RFC3339, expiresAt)
				if err != nil {
					return fmt.Errorf("invalid --expires-at (want RFC3339, e.g. 2027-01-01T00:00:00Z): %w", err)
				}
				expires = &t
			}
			c, err := a.api()
			if err != nil {
				return err
			}
			app, err := c.ResolveApplication(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			s, err := c.CreateSecret(cmd.Context(), app.ID, name, expires)
			if err != nil {
				return err
			}
			if a.jsonOutput() {
				return a.printJSON(s)
			}
			return a.printFields(
				[2]string{"Client ID", app.ID},
				[2]string{"Name", s.Name},
				[2]string{"Secret", s.Value},
			)
		},
	}
	create.Flags().StringVar(&name, "name", "", "secret name")
	create.Flags().StringVar(&expiresAt, "expires-at", "", "expiry time, RFC3339")
	_ = create.MarkFlagRequired("name")

	del := &cobra.Command{
		Use:   "delete <app> <name>",
		Short: "Delete a named secret",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.api()
			if err != nil {
				return err
			}
			app, err := c.ResolveApplication(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := c.DeleteSecret(cmd.Context(), app.ID, args[1]); err != nil {
				return err
			}
			a.infof("Deleted secret %q of application %s.", args[1], app.ID)
			return nil
		},
	}

	cmd.AddCommand(list, create, del)
	return cmd
}

func (a *app) appsRolesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "roles",
		Short: "Manage an M2M application's roles",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list <app>",
			Short: "List an M2M application's roles",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, err := a.api()
				if err != nil {
					return err
				}
				app, err := c.ResolveApplication(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				roles, err := c.ApplicationRoles(cmd.Context(), app.ID)
				if err != nil {
					return err
				}
				return a.printRoles(roles)
			},
		},
		&cobra.Command{
			Use:   "add <app> <role...>",
			Short: "Assign M2M roles to an M2M application (names or IDs)",
			Args:  cobra.MinimumNArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, err := a.api()
				if err != nil {
					return err
				}
				app, err := c.ResolveApplication(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				if app.Type != logto.AppTypeMachineToMachine {
					return fmt.Errorf("application %s is %s; roles can only be assigned to MachineToMachine apps", app.ID, app.Type)
				}
				roleIDs, err := c.ResolveRoleIDs(cmd.Context(), args[1:])
				if err != nil {
					return err
				}
				if err := c.AddApplicationRoles(cmd.Context(), app.ID, roleIDs); err != nil {
					return err
				}
				a.infof("Updated roles of application %s.", app.ID)
				return nil
			},
		},
	)
	return cmd
}
