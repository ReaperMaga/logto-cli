# AGENTS.md

Guidance for AI coding agents (and humans) working on this repository.

## Project

`logto-cli` is a single static Go binary for headless administration of a self-hosted
**Logto** (OSS edition, tenant `default`). It:

- bootstraps the first M2M credential directly in Postgres (`bootstrap`),
- talks to the Logto Management API with client-credentials tokens (`auth`, `users`, `apps`, `roles`),
- offers a raw escape hatch for any endpoint (`api`),
- reconciles users, roles and apps from YAML (`apply`).

The tool is deployment-agnostic. Nomad and Vault are supported as optional integrations
(example job in `deploy/nomad/`, `apply --vault-prefix`), but core code must not depend on them.

`README.md` is the user-facing documentation.

## Layout

```
cmd/logto-cli/        main: signal handling, version via -ldflags "-X main.version=..."
internal/cli/         cobra commands, flag/env resolution, output (table/json), exit codes
internal/logto/       Management API client: token cache, retries, pagination, typed methods
internal/bootstrap/   pgx + SQL against Logto internal tables (one transaction, idempotent)
internal/apply/       YAML spec, plan/diff, execution (depends on the apply.API interface)
test/integration/     end-to-end test, build tag `integration`, uses docker-compose.test.yml
testdata/             example spec (logto.example.yaml)
deploy/nomad/         example Nomad batch job (optional integration)
.github/workflows/    ci.yml, build.yml (reusable cross-compile), release.yml (tag -> release)
```

## Commands

```sh
make build              # bin/logto-cli
make test               # go test ./...
make lint               # go vet (incl. -tags integration) + golangci-lint
make test-integration   # docker compose up (Postgres 17 + Logto) and run the e2e test
make build-all          # cross-compile all release targets
```

Before finishing any change: `gofmt -l .` is empty, `go vet ./...` and `golangci-lint run`
are clean, and `go test ./...` passes. Run the integration test when touching
`internal/bootstrap`, `internal/logto` request shapes, or command behavior, if Docker is available;
otherwise say that it was not run.

## Rules

### Architecture
- Commands never build HTTP requests themselves; add a typed method to `internal/logto`.
- `internal/apply` talks to the API only through the `apply.API` interface; extend the interface
  and the in-memory fake in `apply_test.go` together.
- Typed API structs keep the raw JSON so `-o json` prints exactly what the API returned.
  Follow the existing `UnmarshalJSON`/`MarshalJSON` pattern for new types.
- Keep dependencies minimal: cobra, pgx (bootstrap only), yaml.v3. No viper, no codegen.
  Ask before adding a dependency.

### Logto specifics
- Verify API paths, fields and query params against the Logto source or
  `GET {endpoint}/api/swagger.json` for the pinned version; do not guess.
- Pagination: `page`, `page_size` (max 100), total in the `Total-Number` header.
- `bootstrap` writes Logto **internal** tables. The pinned version is
  `supportedLogtoVersion` in `internal/bootstrap` (keep `docker-compose.test.yml` in sync).
  When bumping it, re-check `packages/schemas/tables/*.sql` for every table used.
- `bootstrap` and `apply` must stay idempotent: a second run with the same input changes nothing.

### CLI behavior
- Every global flag has an env var; a flag beats its env var.
- Exit codes: `0` ok, `1` API/usage error, `2` auth failure, `3` bootstrap/DB failure.
- Never print secrets except where the command's purpose is to emit one; never log the
  Authorization header. Prefer stdin for secrets (`--password-stdin`, `-d @-`).
- Data goes to stdout, status messages and warnings to stderr.
- Destructive commands require confirmation or `--yes`; `--prune` must never delete the CLI's
  own app or its roles.

### Style
- Match the surrounding code: comment density, naming, error wrapping (`fmt.Errorf("...: %w", err)`).
- Tests: `httptest.Server` for the client, the in-memory fake for `apply`, table tests where natural.

## Git conventions

### Commits: Conventional Commits

Format: `<type>(<optional scope>): <summary>` in the imperative, lower case, no trailing period,
at most ~72 characters. Add a body explaining *why* when it is not obvious.

| Type | Use for |
|---|---|
| `feat` | a new command, flag or user-visible capability |
| `fix` | a bug fix |
| `refactor` | code change without behavior change |
| `perf` | performance improvement |
| `test` | adding or fixing tests only |
| `docs` | README, AGENTS.md, comments |
| `build` | Makefile, Dockerfile, go.mod / dependencies |
| `ci` | GitHub workflows |
| `chore` | maintenance that fits nothing above |
| `revert` | reverting a previous commit |

Suggested scopes: `cli`, `client`, `bootstrap`, `apply`, `users`, `apps`, `roles`, `api`,
`release`, `nomad`.

Breaking changes: add `!` after the type/scope (`feat(cli)!: ...`) and a `BREAKING CHANGE:` footer.

Examples:

```
feat(apps): add --expires-at to secrets create
fix(client): stop paginating when Total-Number is missing
refactor(apply): split planner into per-section files
ci: build darwin/arm64 binaries
docs(readme): document exit codes
```

### Branches

Format: `<type>/<short-kebab-description>`, using the same types as commits:

```
feat/apply-organizations
fix/token-refresh-margin
refactor/apply-planner
docs/deployment-examples
ci/release-checksums
chore/bump-logto-1.45
```

Never commit directly to `main`; open a pull request. PR titles follow the commit format,
since squash merges use them as the commit message.

### Releases

Tag `vX.Y.Z` (SemVer) on `main` to trigger `release.yml`. `feat` bumps minor, `fix` bumps patch,
breaking changes bump major (minor while below 1.0). Tags with a `-` suffix (e.g. `v1.2.0-rc.1`)
are published as pre-releases.
