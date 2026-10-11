// Package cli wires the cobra commands, configuration and output formatting.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/reapermaga/logto-cli/internal/logto"
)

// Exit codes.
const (
	ExitOK        = 0
	ExitError     = 1 // API or usage error
	ExitAuth      = 2 // authentication failure
	ExitBootstrap = 3 // bootstrap / database failure
)

// Version is set by main from ldflags.
var Version = "dev"

// exitError carries an explicit exit code.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func withCode(code int, err error) error {
	if err == nil {
		return nil
	}
	return &exitError{code: code, err: err}
}

// silentError makes Run exit non-zero without printing anything more.
type silentError struct{ code int }

func (e *silentError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

// config is the resolved global configuration. A flag beats its env var.
type config struct {
	endpoint  string
	appID     string
	appSecret string
	resource  string
	dbURL     string
	output    string
}

// app holds per-invocation state shared by commands.
type app struct {
	cfg    config
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	client *logto.Client
}

// envFor lists the env vars of each global flag, in priority order.
var envFor = map[string][]string{
	"endpoint":   {"LOGTO_ENDPOINT"},
	"app-id":     {"LOGTO_APP_ID"},
	"app-secret": {"LOGTO_APP_SECRET"},
	"resource":   {"LOGTO_RESOURCE"},
	"db-url":     {"LOGTO_DB_URL", "DB_URL"},
	"output":     {"LOGTO_OUTPUT"},
}

// Run executes the CLI and returns the process exit code.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a := &app{stdin: stdin, stdout: stdout, stderr: stderr}
	root := a.rootCmd()
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	var silent *silentError
	if errors.As(err, &silent) {
		return silent.code
	}
	_, _ = fmt.Fprintln(stderr, "Error:", err)
	return exitCode(err)
}

func exitCode(err error) int {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	var authErr *logto.AuthError
	if errors.As(err, &authErr) {
		return ExitAuth
	}
	return ExitError
}

func (a *app) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "logto-cli",
		Short:         "Headless administration for self-hosted Logto",
		Long:          "logto-cli bootstraps an M2M credential in Postgres and manages users, applications and roles through the Logto Management API.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return a.resolveConfig(cmd.Flags())
		},
	}

	pf := root.PersistentFlags()
	pf.StringVar(&a.cfg.endpoint, "endpoint", "", "Logto core endpoint, e.g. https://auth.example.com [$LOGTO_ENDPOINT]")
	pf.StringVar(&a.cfg.appID, "app-id", "", "M2M app ID [$LOGTO_APP_ID]")
	pf.StringVar(&a.cfg.appSecret, "app-secret", "", "M2M app secret [$LOGTO_APP_SECRET]")
	pf.StringVar(&a.cfg.resource, "resource", "", "Management API resource indicator (default "+logto.DefaultResource+") [$LOGTO_RESOURCE]")
	pf.StringVar(&a.cfg.dbURL, "db-url", "", "Postgres URL used by Logto, bootstrap only [$LOGTO_DB_URL, $DB_URL]")
	pf.StringVarP(&a.cfg.output, "output", "o", "", "output format: table or json (default table) [$LOGTO_OUTPUT]")

	root.AddCommand(
		a.bootstrapCmd(),
		a.authCmd(),
		a.apiCmd(),
		a.usersCmd(),
		a.appsCmd(),
		a.rolesCmd(),
		a.applyCmd(),
	)
	return root
}

func (a *app) resolveConfig(flags *pflag.FlagSet) error {
	targets := map[string]*string{
		"endpoint":   &a.cfg.endpoint,
		"app-id":     &a.cfg.appID,
		"app-secret": &a.cfg.appSecret,
		"resource":   &a.cfg.resource,
		"db-url":     &a.cfg.dbURL,
		"output":     &a.cfg.output,
	}
	for name, target := range targets {
		if flags.Changed(name) {
			continue
		}
		for _, env := range envFor[name] {
			if v, ok := os.LookupEnv(env); ok && v != "" {
				*target = v
				break
			}
		}
	}
	if a.cfg.resource == "" {
		a.cfg.resource = logto.DefaultResource
	}
	switch a.cfg.output {
	case "":
		a.cfg.output = "table"
	case "table", "json":
	default:
		return fmt.Errorf("invalid output format %q: use table or json", a.cfg.output)
	}
	return nil
}

// api returns the Management API client, creating it on first use.
func (a *app) api() (*logto.Client, error) {
	if a.client != nil {
		return a.client, nil
	}
	c, err := logto.New(logto.Config{
		Endpoint:  a.cfg.endpoint,
		AppID:     a.cfg.appID,
		AppSecret: a.cfg.appSecret,
		Resource:  a.cfg.resource,
	})
	if err != nil {
		return nil, err
	}
	a.client = c
	return c, nil
}

func (a *app) jsonOutput() bool { return a.cfg.output == "json" }

func (a *app) warnf(format string, args ...any) {
	_, _ = fmt.Fprintf(a.stderr, "Warning: "+format+"\n", args...)
}

func (a *app) infof(format string, args ...any) {
	_, _ = fmt.Fprintf(a.stderr, format+"\n", args...)
}
