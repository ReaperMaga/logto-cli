package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func (a *app) authCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Management API authentication",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "token",
			Short: "Print a Management API access token",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				c, err := a.api()
				if err != nil {
					return err
				}
				token, err := c.Token(cmd.Context())
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(a.stdout, token)
				return err
			},
		},
		&cobra.Command{
			Use:   "check",
			Short: "Verify that the credential can call the Management API",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				c, err := a.api()
				if err != nil {
					return err
				}
				if _, err := c.Raw(cmd.Context(), "GET", "/api/applications?page=1&page_size=1", nil); err != nil {
					return err
				}
				_, err = fmt.Fprintf(a.stdout, "OK: authenticated to %s as %s\n", a.cfg.endpoint, a.cfg.appID)
				return err
			},
		},
	)
	return cmd
}
