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

## Commands

```
vortex auth signup|login|logout|whoami
vortex auth login --token vrt_...             # store a PAT for CI / non-interactive use
vortex auth token create <name> [--scope ... --expires-in-days N]
vortex auth token list|revoke <token-id>

vortex orgs list|create <name>
vortex projects list|create <name>            # within the current org

vortex apps list
vortex apps create <name> [--image IMG | --git-repo URL --git-branch B] [--cpu --memory]
vortex apps status <id>
vortex apps update <id> [--image --cpu --memory --git-repo --git-branch]
vortex apps scale <id> --min N --max N
vortex apps deploy|restart|stop|destroy <id>
vortex apps rollback <id> [--revision N]
vortex apps releases <id>
vortex apps builds <id> [--logs <build-id>]
vortex apps metrics <id>
vortex apps logs <id> [--follow [--all] | --since SUBSTR --tail N]
vortex apps domains add <id> <domain>
vortex apps domains verify <id> <domain-id>
vortex apps domains list <id>
vortex apps domains remove <id> <domain-id>

vortex services catalog|list
vortex services create <template-key> [--name --cpu --memory]
vortex services deploy|stop|restart|destroy <service-id>

vortex databases list
vortex databases create <name> [--engine --cpu --memory --storage]
vortex databases get <id>                     # shows host/port/db/user/password + connectionString
vortex databases deploy|start|stop|restart <id>
vortex databases delete <id>

vortex secrets list|set KEY=VALUE...[--secret]|unset KEY...   (--app <id>)
vortex plans                                  # billing plan catalog
vortex pricing                                # hourly resource price list
vortex config set-context|show
vortex version [--server]
```

### Examples

```bash
# Pick a working context
vortex orgs list
vortex config set-context --org org_123
vortex projects list

# Deploy an app — directly from a container image, or build from git
vortex apps create web --image nginx:1.27 --cpu 0.5 --memory 512
vortex apps create api --git-repo https://github.com/me/api --git-branch main
vortex apps list
vortex apps deploy <app-id>
vortex apps status <app-id>            # includes the current release
vortex apps logs <app-id> --follow    # live SSE stream
vortex apps logs <app-id> --tail 100 --since ERROR

# Update, scale, roll back
vortex apps update <app-id> --image nginx:1.28 --memory 1024
vortex apps scale <app-id> --min 1 --max 5
vortex apps releases <app-id>
vortex apps rollback <app-id> --revision 3
vortex apps metrics <app-id>

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
│   ├── cmd/                    # cobra command groups (one file per group)
│   │   ├── root.go output.go auth.go orgs.go projects.go
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
