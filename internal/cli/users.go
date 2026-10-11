package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/reapermaga/logto-cli/internal/logto"
)

func (a *app) usersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "users",
		Aliases: []string{"user"},
		Short:   "Manage users",
	}
	cmd.AddCommand(
		a.usersListCmd(),
		a.usersGetCmd(),
		a.usersCreateCmd(),
		a.usersUpdateCmd(),
		a.usersSetPasswordCmd(),
		a.usersSuspendCmd("suspend", true),
		a.usersSuspendCmd("unsuspend", false),
		a.usersDeleteCmd(),
		a.usersRolesCmd(),
	)
	return cmd
}

func (a *app) printUsers(users []logto.User) error {
	if a.jsonOutput() {
		if users == nil {
			users = []logto.User{}
		}
		return a.printJSON(users)
	}
	rows := make([][]string, 0, len(users))
	for _, u := range users {
		rows = append(rows, []string{u.ID, u.Username, u.PrimaryEmail, u.Name, fmtBool(u.IsSuspended), fmtTime(u.LastSignInAt)})
	}
	return a.printTable([]string{"ID", "USERNAME", "EMAIL", "NAME", "SUSPENDED", "LAST SIGN-IN"}, rows)
}

func (a *app) printUser(u *logto.User) error {
	if a.jsonOutput() {
		return a.printJSON(u)
	}
	return a.printFields(
		[2]string{"ID", u.ID},
		[2]string{"Username", u.Username},
		[2]string{"Email", u.PrimaryEmail},
		[2]string{"Phone", u.PrimaryPhone},
		[2]string{"Name", u.Name},
		[2]string{"Avatar", u.Avatar},
		[2]string{"Suspended", fmtBool(u.IsSuspended)},
		[2]string{"Last sign-in", fmtTime(u.LastSignInAt)},
		[2]string{"Created", fmtTime(u.CreatedAt)},
	)
}

func (a *app) usersListCmd() *cobra.Command {
	var (
		search         string
		page, pageSize int
		all            bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List users",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.api()
			if err != nil {
				return err
			}
			s := logto.UserSearch{Search: search}
			if all {
				users, err := c.ListAllUsers(cmd.Context(), s)
				if err != nil {
					return err
				}
				return a.printUsers(users)
			}
			users, total, err := c.ListUsers(cmd.Context(), s, logto.Page{Page: page, PageSize: pageSize})
			if err != nil {
				return err
			}
			if err := a.printUsers(users); err != nil {
				return err
			}
			if !a.jsonOutput() && total > len(users) {
				a.infof("Page %d, %d of %d users (use --page or --all).", page, len(users), total)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&search, "search", "", "fuzzy search over username, email, phone and name")
	f.IntVar(&page, "page", 1, "page number")
	f.IntVar(&pageSize, "page-size", 20, "page size (max 100)")
	f.BoolVar(&all, "all", false, "fetch all pages")
	cmd.MarkFlagsMutuallyExclusive("all", "page")
	return cmd
}

func (a *app) usersGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id|username|email>",
		Short: "Show a user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.api()
			if err != nil {
				return err
			}
			u, err := c.ResolveUser(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return a.printUser(u)
		},
	}
}

// passwordFrom returns the password from --password or --password-stdin.
func (a *app) passwordFrom(cmd *cobra.Command, password string, fromStdin bool) (string, error) {
	if fromStdin {
		if cmd.Flags().Changed("password") {
			return "", errors.New("--password and --password-stdin are mutually exclusive")
		}
		return a.readSecretLine()
	}
	if password != "" {
		a.warnf("--password exposes the password in shell history and process lists; prefer --password-stdin")
	}
	return password, nil
}

func (a *app) usersCreateCmd() *cobra.Command {
	var (
		in        logto.CreateUserInput
		password  string
		fromStdin bool
	)
	cmd := &cobra.Command{
		Use:     "create",
		Short:   "Create a user",
		Example: `  echo "$PASSWORD" | logto-cli users create --username alice --email alice@example.com --password-stdin`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if in.Username == "" && in.PrimaryEmail == "" && in.PrimaryPhone == "" {
				return errors.New("at least one of --username, --email or --phone is required")
			}
			pw, err := a.passwordFrom(cmd, password, fromStdin)
			if err != nil {
				return err
			}
			in.Password = pw
			c, err := a.api()
			if err != nil {
				return err
			}
			u, err := c.CreateUser(cmd.Context(), in)
			if err != nil {
				return err
			}
			return a.printUser(u)
		},
	}
	f := cmd.Flags()
	f.StringVar(&in.Username, "username", "", "username")
	f.StringVar(&in.PrimaryEmail, "email", "", "primary email")
	f.StringVar(&in.PrimaryPhone, "phone", "", "primary phone")
	f.StringVar(&in.Name, "name", "", "display name")
	f.StringVar(&in.Avatar, "avatar", "", "avatar URL")
	f.StringVar(&password, "password", "", "password (prefer --password-stdin)")
	f.BoolVar(&fromStdin, "password-stdin", false, "read the password from the first line of stdin")
	return cmd
}

func (a *app) usersUpdateCmd() *cobra.Command {
	flagToField := [][2]string{
		{"username", "username"},
		{"email", "primaryEmail"},
		{"phone", "primaryPhone"},
		{"name", "name"},
		{"avatar", "avatar"},
	}
	values := map[string]*string{}
	cmd := &cobra.Command{
		Use:   "update <id|username|email>",
		Short: "Update a user's basic fields (an empty value clears the field)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fields := map[string]any{}
			for _, ff := range flagToField {
				if cmd.Flags().Changed(ff[0]) {
					fields[ff[1]] = *values[ff[0]]
				}
			}
			if len(fields) == 0 {
				return errors.New("nothing to update: pass at least one of --username, --email, --phone, --name, --avatar")
			}
			c, err := a.api()
			if err != nil {
				return err
			}
			u, err := c.ResolveUser(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			u, err = c.UpdateUser(cmd.Context(), u.ID, fields)
			if err != nil {
				return err
			}
			return a.printUser(u)
		},
	}
	for _, ff := range flagToField {
		values[ff[0]] = cmd.Flags().String(ff[0], "", "new "+ff[0])
	}
	return cmd
}

func (a *app) usersSetPasswordCmd() *cobra.Command {
	var (
		password  string
		fromStdin bool
	)
	cmd := &cobra.Command{
		Use:   "set-password <id|username|email>",
		Short: "Set a user's password",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pw, err := a.passwordFrom(cmd, password, fromStdin)
			if err != nil {
				return err
			}
			if pw == "" {
				return errors.New("a password is required: use --password-stdin or --password")
			}
			c, err := a.api()
			if err != nil {
				return err
			}
			u, err := c.ResolveUser(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := c.SetUserPassword(cmd.Context(), u.ID, pw); err != nil {
				return err
			}
			a.infof("Password updated for user %s.", u.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&password, "password", "", "new password (prefer --password-stdin)")
	cmd.Flags().BoolVar(&fromStdin, "password-stdin", false, "read the password from the first line of stdin")
	return cmd
}

func (a *app) usersSuspendCmd(name string, suspended bool) *cobra.Command {
	return &cobra.Command{
		Use:   name + " <id|username|email>",
		Short: map[bool]string{true: "Suspend a user", false: "Unsuspend a user"}[suspended],
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.api()
			if err != nil {
				return err
			}
			u, err := c.ResolveUser(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			u, err = c.SetUserSuspended(cmd.Context(), u.ID, suspended)
			if err != nil {
				return err
			}
			if a.jsonOutput() {
				return a.printJSON(u)
			}
			a.infof("User %s suspended: %s.", u.ID, fmtBool(u.IsSuspended))
			return nil
		},
	}
}

func (a *app) usersDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id|username|email>",
		Short: "Delete a user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.api()
			if err != nil {
				return err
			}
			u, err := c.ResolveUser(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !yes {
				ok, err := a.confirm(fmt.Sprintf("Delete user %s (%s)?", u.ID, firstNonEmpty(u.Username, u.PrimaryEmail, u.PrimaryPhone)))
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("aborted")
				}
			}
			if err := c.DeleteUser(cmd.Context(), u.ID); err != nil {
				return err
			}
			a.infof("Deleted user %s.", u.ID)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

func (a *app) usersRolesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "roles",
		Short: "Manage a user's roles",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list <user>",
			Short: "List a user's roles",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, err := a.api()
				if err != nil {
					return err
				}
				u, err := c.ResolveUser(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				roles, err := c.UserRoles(cmd.Context(), u.ID)
				if err != nil {
					return err
				}
				return a.printRoles(roles)
			},
		},
		a.userRolesMutateCmd("add <user> <role...>", "Assign roles to a user (names or IDs)", cobra.MinimumNArgs(2), func(cmd *cobra.Command, c *logto.Client, userID string, roleIDs []string) error {
			return c.AddUserRoles(cmd.Context(), userID, roleIDs)
		}),
		a.userRolesMutateCmd("set <user> <role...>", "Replace a user's roles (names or IDs)", cobra.MinimumNArgs(2), func(cmd *cobra.Command, c *logto.Client, userID string, roleIDs []string) error {
			return c.SetUserRoles(cmd.Context(), userID, roleIDs)
		}),
		a.userRolesMutateCmd("remove <user> <role>", "Remove a role from a user", cobra.ExactArgs(2), func(cmd *cobra.Command, c *logto.Client, userID string, roleIDs []string) error {
			return c.RemoveUserRole(cmd.Context(), userID, roleIDs[0])
		}),
	)
	return cmd
}

func (a *app) userRolesMutateCmd(use, short string, args cobra.PositionalArgs, fn func(*cobra.Command, *logto.Client, string, []string) error) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  args,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.api()
			if err != nil {
				return err
			}
			u, err := c.ResolveUser(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			roleIDs, err := c.ResolveRoleIDs(cmd.Context(), args[1:])
			if err != nil {
				return err
			}
			if err := fn(cmd, c, u.ID, roleIDs); err != nil {
				return err
			}
			a.infof("Updated roles of user %s.", u.ID)
			return nil
		},
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
