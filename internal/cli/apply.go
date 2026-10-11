package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/reapermaga/logto-cli/internal/apply"
)

func (a *app) applyCmd() *cobra.Command {
	var (
		file        string
		dryRun      bool
		prune       bool
		yes         bool
		vaultPrefix string
	)
	cmd := &cobra.Command{
		Use:   "apply -f logto.yaml",
		Short: "Reconcile roles, users and apps with a declarative YAML file",
		Long: `Creates or updates the roles, users and applications declared in the file,
matched by name (users by username, or email when no username is given).
${VAR} references in the file are expanded from the environment.

--prune also deletes undeclared items of each section present in the file, and
removes undeclared role/scope assignments. The CLI's own app and its roles are
never pruned.

Secrets of newly created confidential apps are printed to stdout, or written
with "vault kv put <prefix>/<app name>" when --vault-prefix is set.`,
		Example: `  logto-cli apply -f logto.yaml --dry-run
  logto-cli apply -f logto.yaml --vault-prefix secret/default/logto/apps`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			spec, err := apply.LoadFile(file, a.stdin)
			if err != nil {
				return err
			}
			if vaultPrefix != "" && !dryRun {
				if _, err := exec.LookPath("vault"); err != nil {
					return errors.New("--vault-prefix needs the vault CLI on PATH")
				}
			}
			c, err := a.api()
			if err != nil {
				return err
			}
			plan, err := apply.Build(cmd.Context(), c, spec, apply.Options{Prune: prune})
			if err != nil {
				return err
			}
			plan.Write(a.stdout)
			if dryRun || plan.Empty() {
				return nil
			}

			deletes := 0
			for _, ch := range plan.Changes {
				if ch.Action == apply.Delete {
					deletes++
				}
			}
			if deletes > 0 && !yes {
				ok, err := a.confirm(fmt.Sprintf("Apply, including %d deletion(s)?", deletes))
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("aborted")
				}
			}

			_, _ = fmt.Fprintln(a.stdout)
			secrets, execErr := plan.Execute(cmd.Context(), a.stdout)
			// Report secrets even after a failure, or they would be lost.
			if err := a.emitSecrets(cmd.Context(), secrets, vaultPrefix); err != nil {
				return errors.Join(execErr, err)
			}
			if execErr != nil {
				return execErr
			}
			_, _ = fmt.Fprintln(a.stdout, "Apply complete.")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&file, "file", "f", "", "spec file, or - for stdin")
	f.BoolVar(&dryRun, "dry-run", false, "print the diff without changing anything")
	f.BoolVar(&prune, "prune", false, "delete undeclared items in the sections present in the file")
	f.BoolVarP(&yes, "yes", "y", false, "do not ask before deleting")
	f.StringVar(&vaultPrefix, "vault-prefix", "", "write new app secrets with vault kv put <prefix>/<app name>")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func (a *app) emitSecrets(ctx context.Context, secrets []apply.CreatedSecret, vaultPrefix string) error {
	if len(secrets) == 0 {
		return nil
	}
	if vaultPrefix != "" {
		var errs []error
		for _, s := range secrets {
			path := strings.TrimRight(vaultPrefix, "/") + "/" + s.App
			if err := vaultPut(ctx, path, s.AppID, s.Secret); err != nil {
				errs = append(errs, fmt.Errorf("vault kv put %s: %w (client ID %s; read the secret with: logto-cli apps secrets list %s)", path, err, s.AppID, s.AppID))
				continue
			}
			_, _ = fmt.Fprintf(a.stdout, "Wrote credentials of app %q (%s) to %s.\n", s.App, s.AppID, path)
		}
		return errors.Join(errs...)
	}
	if a.jsonOutput() {
		out := make([]map[string]string, 0, len(secrets))
		for _, s := range secrets {
			out = append(out, map[string]string{"app": s.App, "clientId": s.AppID, "clientSecret": s.Secret})
		}
		return a.printJSON(out)
	}
	_, _ = fmt.Fprintln(a.stdout, "\nNew application credentials (shown once):")
	rows := make([][]string, 0, len(secrets))
	for _, s := range secrets {
		rows = append(rows, []string{s.App, s.AppID, s.Secret})
	}
	return a.printTable([]string{"APP", "CLIENT ID", "CLIENT SECRET"}, rows)
}

// vaultPut stores the credential with the vault CLI. The secret is passed on
// stdin so it does not show up in the process list.
func vaultPut(ctx context.Context, path, clientID, secret string) error {
	cmd := exec.CommandContext(ctx, "vault", "kv", "put", path, "CLIENT_ID="+clientID, "CLIENT_SECRET=-")
	cmd.Stdin = strings.NewReader(secret)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
