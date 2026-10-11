package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/reapermaga/logto-cli/internal/logto"
)

func (a *app) apiCmd() *cobra.Command {
	var data string
	cmd := &cobra.Command{
		Use:   "api METHOD PATH",
		Short: "Call any Management API endpoint (raw escape hatch)",
		Long: `Sends an authenticated request and prints the response body (pretty JSON).
Exits non-zero on 4xx/5xx. PATH may include a query string.

The body (-d) is a JSON string, @file to read a file, or @- to read stdin.`,
		Example: `  logto-cli api GET /api/organizations
  logto-cli api POST /api/organizations -d '{"name":"acme"}'
  logto-cli api PATCH /api/users/abc -d @body.json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			method, path := strings.ToUpper(args[0]), args[1]
			if !strings.HasPrefix(path, "/") {
				return fmt.Errorf("path must start with /, got %q", path)
			}
			body, err := a.readBody(data)
			if err != nil {
				return err
			}
			c, err := a.api()
			if err != nil {
				return err
			}
			resp, err := c.Raw(cmd.Context(), method, path, body)
			var apiErr *logto.APIError
			if errors.As(err, &apiErr) && resp != nil {
				// Print the error body like any response, then fail.
				_ = a.printRawJSON(resp.Body)
				return err
			}
			if err != nil {
				return err
			}
			return a.printRawJSON(resp.Body)
		},
	}
	cmd.Flags().StringVarP(&data, "data", "d", "", "request body: JSON, @file, or @- for stdin")
	return cmd
}

func (a *app) readBody(data string) ([]byte, error) {
	switch {
	case data == "":
		return nil, nil
	case data == "@-":
		b, err := io.ReadAll(a.stdin)
		if err != nil {
			return nil, fmt.Errorf("read body from stdin: %w", err)
		}
		return b, nil
	case strings.HasPrefix(data, "@"):
		b, err := os.ReadFile(data[1:])
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
		return b, nil
	default:
		return []byte(data), nil
	}
}
