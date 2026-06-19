# vortex CLI

`vortex` is the command-line interface for the **Vortex** hosting platform — a
flyctl-equivalent tool that talks to the Vortex control-plane HTTP API
(`apps/api`). It mirrors the fly.io UX: log in, then manage organizations,
projects, apps, one-click services, secrets and billing plans from your terminal.

## Install

From the module directory:

```bash
cd apps/cli
go build -o vortex ./cmd/vortex
# optionally install onto your PATH:
go install ./cmd/vortex   # installs $(go env GOPATH)/bin/vortex
```

Stamp a version into the binary at build time:

```bash
go build -ldflags "-X github.com/mohamed3laa33/viro-coolify/apps/cli/internal/version.Version=v0.1.0 \
  -X github.com/mohamed3laa33/viro-coolify/apps/cli/internal/version.Commit=$(git rev-parse --short HEAD)" \
  -o vortex ./cmd/vortex
```

## Configuration

Config lives at `~/.vortex/config.yaml` (override with `VORTEX_CONFIG`). It is
written with `0600` permissions because it stores bearer tokens.

```yaml
api_url: http://localhost:8080
access_token: "..."       # JWT session (email/password login)
refresh_token: "..."      # JWT refresh token
token: "vrt_..."          # personal access token (when logged in with --token)
current_org: org_123
current_project: proj_456
```

Set the API URL and default context:

```bash
vortex config set-context --api-url https://api.vortex.v60ai.com --org org_123 --project proj_456
vortex config show
```

The persistent `--api-url`, `--org`, `--project` and `--json` flags work on any
command. `--org` / `--project` override the persisted context for a single
invocation.

## Authentication

```bash
vortex auth signup --email you@example.com --name "You"   # password prompted
vortex auth login  --email you@example.com                # password prompted
vortex auth whoami
vortex auth logout
```

JWT tokens are stored in the config file. When the **access token expires**, the
CLI automatically calls `POST /v1/auth/refresh` with the stored refresh token on
a `401`, persists the new token pair, and transparently retries the request. If
the refresh token is also invalid, you are told to run `vortex auth login`.

### Personal access tokens (PAT) — non-interactive / CI

A personal access token is a long-lived `vrt_...` string that authenticates as
its owner on **all** the same endpoints (`Authorization: Bearer vrt_...`). PATs
are never refreshed.

```bash
# Create a token (the secret is shown ONCE — store it now)
vortex auth token create ci --scope deploy --expires-in-days 90
vortex auth token list                       # never shows the secret
vortex auth token revoke <token-id>

# Log in non-interactively in CI (stores the PAT, clears any JWT session)
vortex auth login --token vrt_xxxxxxxxxxxxxxxx
# or: export the token in the config and run any command
```

`POST /v1/tokens` returns the plaintext token only on create; `GET /v1/tokens`
and the listing command never reveal it.

## Names, not UUIDs

Every command that targets an org, project or app accepts a **name, slug, or
id** and resolves it to the canonical id via the API — you never have to copy a
UUID. Resolution prefers an exact id match (so existing id-based scripts keep
working), then a slug, then a case-insensitive name; an unknown or ambiguous
name is a clear error rather than a silent guess. `--json` output is unaffected.

```bash
vortex config set-context --org acme --project staging   # by name
vortex apps deploy web                                    # app by name
vortex --org acme apps status api                         # one-off override by name
```

## One-command launch

`vortex launch` is the flyctl-style on-ramp. From a directory it:

1. **detects** the app — a `Dockerfile` selects the image/build path; a git
   remote (`.git`) is offered as the build source; otherwise it prompts for a
   container image or git repo;
2. **scaffolds** a minimal `vortex.yaml` manifest capturing your choices;
3. **creates** the app in your current org/project via the API (re-running is
   idempotent — it reuses an existing app + manifest instead of recreating); and
4. **deploys** it with a real API deploy (pass `--no-deploy` to skip).

It never bakes in business values: `cpu`/`memoryMb` are left unset so the
control plane applies your plan's admin-configured defaults.

```bash
cd ./my-service
vortex launch                        # interactive: detect, confirm, deploy
vortex launch --image nginx:1.27 -y  # non-interactive (CI): explicit image, no prompts
vortex launch --git-repo https://github.com/me/api --git-branch main -y
vortex launch --no-deploy            # scaffold + create only
```

The generated `vortex.yaml`:

```yaml
app: my-service
build:
  image: nginx:1.27        # or gitRepository/gitBranch for source builds
# cpu / memoryMb omitted on purpose — defaults come from your plan via the API
```

## Commands

```
vortex launch [path] [--name --image | --git-repo --git-branch] [--no-deploy] [-y]

vortex auth signup|login|logout|whoami
vortex auth login --token vrt_...             # store a PAT for CI / non-interactive use
vortex auth token create <name> [--scope ... --expires-in-days N]
vortex auth token list|revoke <token-id>

vortex orgs list|create <name>
vortex projects list|create <name>            # within the current org

vortex apps list                              # <app> below = app name or id
vortex apps create <name> [--image IMG | --git-repo URL --git-branch B] [--cpu --memory]
vortex apps status <app>
vortex apps update <app> [--image --cpu --memory --git-repo --git-branch]
vortex apps scale <app> --min N --max N
vortex apps deploy|restart|stop|destroy <app>
vortex apps rollback <app> [--revision N]
vortex apps releases <app>
vortex apps builds <app> [--logs <build-id>]
vortex apps metrics <app>
vortex apps logs <app> [--follow [--all] | --since SUBSTR --tail N]
vortex apps domains add <app> <domain>
vortex apps domains verify <app> <domain-id>
vortex apps domains list <app>
vortex apps domains remove <app> <domain-id>

vortex services catalog|list
vortex services create <template-key> [--name --cpu --memory]
vortex services deploy|stop|restart|destroy <service-id>

vortex databases list
vortex databases create <name> [--engine --cpu --memory --storage]
vortex databases get <id>                     # shows host/port/db/user/password + connectionString
vortex databases deploy|start|stop|restart <id>
vortex databases delete <id>

vortex secrets list|set KEY=VALUE...[--secret]|unset KEY...   (--app <app>)
vortex plans                                  # billing plan catalog
vortex pricing                                # hourly resource price list
vortex config set-context|show
vortex version [--server]
```

### Examples

```bash
# Zero-to-deployed in one command
vortex launch                          # from the current directory

# Pick a working context (by name or id)
vortex orgs list
vortex config set-context --org acme
vortex projects list

# Deploy an app — directly from a container image, or build from git
vortex apps create web --image nginx:1.27 --cpu 0.5 --memory 512
vortex apps create api --git-repo https://github.com/me/api --git-branch main
vortex apps list
vortex apps deploy web                 # by name (or id)
vortex apps status web                 # includes the current release
vortex apps logs web --follow          # live SSE stream
vortex apps logs web --tail 100 --since ERROR

# Update, scale, roll back
vortex apps update web --image nginx:1.28 --memory 1024
vortex apps scale web --min 1 --max 5
vortex apps releases web
vortex apps rollback web --revision 3
vortex apps metrics web

# Builds (git-source)
vortex apps builds <app-id>
vortex apps builds <app-id> --logs <build-id>

# Custom domains (prints the TXT + A/CNAME records to publish)
vortex apps domains add <app-id> app.example.com
vortex apps domains verify <app-id> <domain-id>

# Secrets (app env vars); --secret encrypts at rest + masks on read
vortex secrets set DATABASE_URL=postgres://... --app <app-id> --secret
vortex secrets set LOG_LEVEL=info --app <app-id>
vortex secrets list --app <app-id>
vortex secrets unset LOG_LEVEL --app <app-id>

# One-click services
vortex services catalog
vortex services create wordpress --name blog
vortex services deploy <service-id>

# Managed databases (get shows the connection info)
vortex databases create maindb --engine postgresql --storage 10
vortex databases get <db-id>

# Billing / scripting
vortex plans
vortex pricing
vortex apps list --json | jq '.[].id'
```

Add `--json` to any command for machine-readable output suitable for scripting.

## Architecture

```
apps/cli/
├── cmd/vortex/main.go          # entrypoint
├── internal/
│   ├── client/                 # typed API client over net/http
│   │   ├── client.go           # request plumbing + auto token refresh on 401
│   │   ├── api.go              # one method per endpoint
│   │   ├── types.go            # request/response models (match apps/api)
│   │   └── client_test.go      # httptest-based unit tests
│   ├── config/                 # ~/.vortex/config.yaml load/save
│   │   ├── config.go
│   │   └── config_test.go
│   ├── manifest/               # per-directory vortex.yaml (launch)
│   │   ├── manifest.go         # read/write the manifest (no business values)
│   │   ├── detect.go           # Dockerfile / git / language detection
│   │   └── manifest_test.go
│   ├── cmd/                    # cobra command groups (one file per group)
│   │   ├── root.go output.go auth.go orgs.go projects.go
│   │   ├── launch.go           # `vortex launch` one-command on-ramp
│   │   ├── resolve.go          # name/slug/id → id resolution (+resolve_test.go)
│   │   ├── apps.go domains.go databases.go services.go
│   │   ├── secrets.go plans.go config.go version.go
│   └── version/version.go      # build-stamped version
└── README.md
```

The API client maps exactly onto the control-plane routes
(`/v1/auth/*`, `/v1/tokens`, `/v1/orgs/{orgID}/...` for apps, services,
databases, env, domains, releases, builds and metrics, `/v1/services/catalog`,
`/v1/billing/plans`, `/v1/billing/pricing`, `/v1/me`, `/v1/version`). Auth is
either a JWT session (auto-refreshed on `401`) or a `vrt_...` personal access
token sent as `Authorization: Bearer`. The CLI never invents endpoints.

## Develop / test

```bash
cd apps/cli
go mod tidy        # resolve dependencies (cobra, golang.org/x/term, yaml.v3)
go build ./...
go vet ./...
go test ./...
```
