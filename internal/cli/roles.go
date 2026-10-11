package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/reapermaga/logto-cli/internal/logto"
)

// parseRoleType maps user|m2m (or the API enum value) to the API enum.
func parseRoleType(s string) (string, error) {
	switch strings.ToLower(s) {
	case "user":
		return logto.RoleTypeUser, nil
	case "m2m", "machinetomachine":
		return logto.RoleTypeMachineToMachine, nil
	}
	return "", fmt.Errorf("invalid role type %q: use user or m2m", s)
}

func (a *app) rolesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "roles",
		Aliases: []string{"role"},
		Short:   "Manage roles",
	}
	cmd.AddCommand(a.rolesListCmd(), a.rolesCreateCmd(), a.rolesDeleteCmd(), a.rolesScopesCmd())
	return cmd
}

func (a *app) printRoles(roles []logto.Role) error {
	if a.jsonOutput() {
		if roles == nil {
			roles = []logto.Role{}
		}
		return a.printJSON(roles)
	}
	count := func(n *int) string {
		if n == nil {
			return ""
		}
		return strconv.Itoa(*n)
	}
	rows := make([][]string, 0, len(roles))
	for _, r := range roles {
		rows = append(rows, []string{r.ID, r.Name, r.Type, r.Description, count(r.UsersCount), count(r.ApplicationsCount)})
	}
	return a.printTable([]string{"ID", "NAME", "TYPE", "DESCRIPTION", "USERS", "APPS"}, rows)
}

func (a *app) rolesListCmd() *cobra.Command {
	var roleType string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List roles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			apiType := ""
			if roleType != "" {
				var err error
				if apiType, err = parseRoleType(roleType); err != nil {
					return err
				}
			}
			c, err := a.api()
			if err != nil {
				return err
			}
			roles, err := c.ListRoles(cmd.Context(), apiType)
			if err != nil {
				return err
			}
			return a.printRoles(roles)
		},
	}
	cmd.Flags().StringVar(&roleType, "type", "", "filter by type: user or m2m")
	return cmd
}

func (a *app) rolesCreateCmd() *cobra.Command {
	var in logto.CreateRoleInput
	var roleType string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a role",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var err error
			if in.Type, err = parseRoleType(roleType); err != nil {
				return err
			}
			if in.Description == "" {
				in.Description = in.Name
			}
			c, err := a.api()
			if err != nil {
				return err
			}
			r, err := c.CreateRole(cmd.Context(), in)
			if err != nil {
				return err
			}
			return a.printRoles([]logto.Role{*r})
		},
	}
	f := cmd.Flags()
	f.StringVar(&in.Name, "name", "", "role name")
	f.StringVar(&in.Description, "description", "", "description (defaults to the name; the API requires one)")
	f.StringVar(&roleType, "type", "user", "user or m2m")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func (a *app) rolesDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id|name>",
		Short: "Delete a role",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.api()
			if err != nil {
				return err
			}
			r, err := c.ResolveRole(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !yes {
				ok, err := a.confirm(fmt.Sprintf("Delete role %s (%s)?", r.ID, r.Name))
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("aborted")
				}
			}
			if err := c.DeleteRole(cmd.Context(), r.ID); err != nil {
				return err
			}
			a.infof("Deleted role %s.", r.ID)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

func (a *app) rolesScopesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scopes",
		Short: "Manage a role's scopes (permissions)",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list <role>",
			Short: "List a role's scopes",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, err := a.api()
				if err != nil {
					return err
				}
				r, err := c.ResolveRole(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				scopes, err := c.RoleScopes(cmd.Context(), r.ID)
				if err != nil {
					return err
				}
				if a.jsonOutput() {
					if scopes == nil {
						scopes = []logto.Scope{}
					}
					return a.printJSON(scopes)
				}
				rows := make([][]string, 0, len(scopes))
				for _, s := range scopes {
					rows = append(rows, []string{s.ID, s.Name, s.ResourceID, s.Description})
				}
				return a.printTable([]string{"ID", "NAME", "RESOURCE", "DESCRIPTION"}, rows)
			},
		},
		&cobra.Command{
			Use:   "add <role> <scopeId...>",
			Short: "Grant scopes to a role",
			Args:  cobra.MinimumNArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, err := a.api()
				if err != nil {
					return err
				}
				r, err := c.ResolveRole(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				if err := c.AddRoleScopes(cmd.Context(), r.ID, args[1:]); err != nil {
					return err
				}
				a.infof("Granted %d scope(s) to role %s.", len(args)-1, r.ID)
				return nil
			},
		},
	)
	return cmd
}
