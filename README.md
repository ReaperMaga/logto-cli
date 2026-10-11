# logto-cli

Manage a self-hosted [Logto](https://github.com/logto-io/logto) (OSS) instance from the command line, without the admin console.

- **`bootstrap`** creates the first machine-to-machine (M2M) credential directly in Postgres, so you never need the web console.
- **`users`, `apps`, `roles`** manage users, applications (including secrets), roles and role assignments through the Management API.
- **`api`** calls any Management API endpoint, including ones without a dedicated command.
- **`apply`** reconciles users, roles and apps with a declarative YAML file.

It ships as a single static binary for Linux, macOS and Windows (amd64 and arm64), plus a `FROM scratch` container image.

## Install

Download a binary from the [releases page](https://github.com/reapermaga/logto-cli/releases) and verify it against `checksums.txt`. Or:

```sh
go install github.com/reapermaga/logto-cli/cmd/logto-cli@latest
docker run --rm ghcr.io/reapermaga/logto-cli:<version> --help
```

## Configuration

Each flag has a matching environment variable. A flag beats its environment variable.

| Flag | Env | Used by | Notes |
|---|---|---|---|
| `--endpoint` | `LOGTO_ENDPOINT` | API commands | Logto core endpoint, e.g. `https://auth.example.com` |
| `--app-id` | `LOGTO_APP_ID` | API commands, `bootstrap` | M2M app ID: at most 21 characters, `[a-z0-9]` |
| `--app-secret` | `LOGTO_APP_SECRET` | API commands, `bootstrap` | at most 64 characters |
| `--resource` | `LOGTO_RESOURCE` | API commands | default `https://default.logto.app/api`, which is fixed for OSS and is **not** your public domain |
| `--db-url` | `LOGTO_DB_URL`, then `DB_URL` | `bootstrap` | the Postgres URL Logto itself uses |
| `-o, --output` | `LOGTO_OUTPUT` | all | `table` (default) or `json`. `json` prints the raw API JSON, ready for `jq` |

Secrets are printed only by commands whose purpose is to show one: `bootstrap --generate`, `apps create`, `apps secrets create/list` and `apply` (for apps it creates). The Authorization header is never logged.

## Quick start

```sh
# 1. Create the first M2M credential (run after Logto has seeded its database).
logto-cli bootstrap --db-url "$DB_URL" --generate --endpoint https://auth.example.com
# LOGTO_APP_ID=...
# LOGTO_APP_SECRET=...

export LOGTO_ENDPOINT=https://auth.example.com LOGTO_APP_ID=... LOGTO_APP_SECRET=...

# 2. Check that the credential works.
logto-cli auth check

# 3. Manage things.
echo "$PW" | logto-cli users create --username alice --email alice@example.com --password-stdin
logto-cli roles create --name admin --type user
logto-cli users roles add alice admin
logto-cli apps create --name grafana --type traditional \
  --redirect-uri https://grafana.example.com/login/generic_oauth
```

## Commands

### `bootstrap`

```sh
logto-cli bootstrap --db-url ... --app-id ... --app-secret ... [--name nomad-bootstrap]
logto-cli bootstrap --db-url ... --generate     # generates the ID and secret, prints them once
```

A fresh Logto has no M2M app and no API key, but its seed creates an M2M role with scope `all` on the Management API. `bootstrap` does the following in **one transaction**:

1. Finds that role. If it is missing, it exits with code 3 ("Logto not seeded yet?").
2. Inserts a `MachineToMachine` application with the given ID and secret.
3. Stores the secret as an application secret named `bootstrap`.
4. Links the application to the role.

The command is idempotent: a second run with the same input changes nothing. If the app exists with a *different* secret, it prints a warning and leaves the secret unchanged. Pass `--rotate` to overwrite it. When `--endpoint` is set, it then verifies the credential by fetching a token and reading the app through the API.

> These are Logto **internal** tables. The SQL was checked against the schema of **Logto 1.44.0** (`supportedLogtoVersion` in `internal/bootstrap`). Check `packages/schemas/tables/*.sql` again before you upgrade Logto.

`--generate` prints `LOGTO_APP_ID=...` and `LOGTO_APP_SECRET=...` on stdout. Status messages go to stderr, so you can pipe stdout straight into Vault:

```sh
logto-cli bootstrap --generate | xargs vault kv put secret/default/logto/m2m
```

### `auth`

```sh
logto-cli auth token    # print an access token (for curl and debugging)
logto-cli auth check    # fetch a token and call the API, print OK
```

The CLI requests tokens with client credentials from `{endpoint}/oidc/token`, keeps them in memory only, and refreshes them 60 seconds before they expire.

### `users`

| Command | Description |
|---|---|
| `users list [--search q] [--page n --page-size n \| --all]` | list users, or search them |
| `users get <id\|username\|email>` | show one user |
| `users create --username --email --phone --name [--password-stdin \| --password]` | create a user |
| `users update <user> [--username --email --phone --name --avatar]` | change fields; an empty value clears the field |
| `users set-password <user> [--password-stdin]` | set the password |
| `users suspend <user>` / `users unsuspend <user>` | suspend or reactivate |
| `users delete <user> [--yes]` | delete |
| `users roles list\|add\|set\|remove <user> <role...>` | manage role assignments; roles by name or ID |

Use `--password-stdin` where you can. `--password` works too, but prints a warning, because it exposes the password in shell history and process lists.

### `apps`

| Command | Description |
|---|---|
| `apps list [--type traditional,spa,native,m2m] [--search q]` | list applications |
| `apps get <id\|name>` | show one application |
| `apps create --name --type traditional\|spa\|native\|m2m [--redirect-uri ...] [--post-logout-uri ...] [--cors-origin ...] [--description]` | create; prints the client ID and secret for `traditional` and `m2m` |
| `apps update <app> [same flags]` | update; a list flag replaces the whole list |
| `apps delete <app> [--yes]` | delete |
| `apps secrets list\|create\|delete <app> ...` | manage named secrets (`create --name n [--expires-at RFC3339]`) |
| `apps roles list\|add <app> <role...>` | manage M2M role assignments (M2M apps only) |

### `roles`

| Command | Description |
|---|---|
| `roles list [--type user\|m2m]` | list roles |
| `roles create --name [--description] [--type user\|m2m]` | create a role |
| `roles delete <id\|name> [--yes]` | delete |
| `roles scopes list <role>` / `roles scopes add <role> <scopeId...>` | manage the scopes (permissions) of a role |

### `api`: raw escape hatch

```sh
logto-cli api GET /api/organizations
logto-cli api POST /api/organizations -d '{"name":"acme"}'
logto-cli api PATCH /api/users/abc -d @body.json
cat body.json | logto-cli api PUT /api/users/abc/roles -d @-
```

The command adds authentication and prints the response body as pretty JSON. It exits non-zero on 4xx and 5xx responses. For paths and fields, see the [Management API reference](https://openapi.logto.io) or `GET {endpoint}/api/swagger.json`.

### `apply`: declarative configuration

```sh
logto-cli apply -f logto.yaml --dry-run        # show the diff only
logto-cli apply -f logto.yaml                  # create and update
logto-cli apply -f logto.yaml --prune --yes    # also delete what is not listed
logto-cli apply -f logto.yaml --vault-prefix secret/default/logto/apps
```

See [`testdata/logto.example.yaml`](testdata/logto.example.yaml) for an example spec. How `apply` behaves:

- It matches roles and apps by name, and users by username (or by email when a user has no username).
- It only manages the fields a spec sets. Leave a field out and Logto keeps its value. For lists, `[]` clears the list.
- It expands `${VAR}` references in values from the environment and fails if a variable is unset. Comments are not expanded.
- It sets `password` only when it creates a user.
- `--prune` deletes undeclared items, but only in sections the file contains. It also removes role and scope assignments the file does not list. It never touches the app the CLI is logged in as or that app's roles.
- It prints the secrets of confidential apps it creates once, or writes them with `vault kv put <prefix>/<app name> CLIENT_ID=... CLIENT_SECRET=-`. The secret goes to Vault on stdin, so it never appears in a process list.
- When there is nothing to change, it prints `No changes.`

## Exit codes

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | API or usage error |
| 2 | authentication failure: token request rejected, or HTTP 401/403 |
| 3 | bootstrap or database failure |

## Nomad integration

These are the cluster conventions this tool assumes: Vault KV v2 at `secret/<namespace>/<job>/...`, and each job file sets `namespace` explicitly.

- **Credential storage:** keep the bootstrap credential at `secret/default/logto/m2m`, under the keys `LOGTO_APP_ID` and `LOGTO_APP_SECRET`.
- **Bootstrap job:** [`deploy/nomad/logto-bootstrap.nomad.hcl`](deploy/nomad/logto-bootstrap.nomad.hcl) is an example `batch` job. It renders `LOGTO_DB_URL`, `LOGTO_APP_ID` and `LOGTO_APP_SECRET` from Vault, then runs `logto-cli bootstrap`. If Logto has not finished `db seed` yet, the command exits with code 3 and Nomad retries it. You can run the same task as a `poststart` task in the Logto job instead.
- **Image:** the release workflow pushes `ghcr.io/reapermaga/logto-cli` and writes the image digest to the run summary. Pin the job's image to that digest.

> The job file is an example. Review the Vault paths, the endpoint and the image digest before you run it.

## Development

```sh
make build              # bin/logto-cli
make test               # unit tests (httptest-based client tests, apply with an in-memory API)
make test-integration   # starts Postgres 17 + Logto 1.44.0 with docker compose and runs the end-to-end test
make lint               # go vet + golangci-lint
make build-all          # cross-compile every release target into bin/
```

The integration test (`test/integration`, build tag `integration`) covers this flow: bootstrap with `--generate`, `auth check`, the raw API, a user's lifecycle (create, add role, suspend, delete), a Traditional app's lifecycle (create, read its secret, delete), running `apply` twice, and re-running bootstrap to confirm it is a no-op.

## Releasing

Push a tag:

```sh
git tag v0.1.0 && git push origin v0.1.0
```

The `Release` workflow then does the following:

1. Runs vet and the tests.
2. Cross-compiles `linux`, `darwin` and `windows` binaries for `amd64` and `arm64`. They are static, built with `CGO_ENABLED=0`, and have the version stamped in.
3. Creates the GitHub release with `.tar.gz` and `.zip` archives and `checksums.txt`.
4. Pushes the multi-arch container image to GHCR.

You can also re-run it for an existing tag from the Actions tab (`workflow_dispatch`).

CI (`ci.yml`) runs on every push and pull request. It runs lint, the unit tests and the dockerized integration test, and uploads the cross-compiled binaries as workflow artifacts.

## License

[MIT](LICENSE) © ReaperMaga
