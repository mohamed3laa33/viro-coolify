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
access_token: "..."
refresh_token: "..."
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

Tokens are stored in the config file. When the **access token expires**, the CLI
automatically calls `POST /v1/auth/refresh` with the stored refresh token on a
`401`, persists the new token pair, and transparently retries the request. If
the refresh token is also invalid, you are told to run `vortex auth login`.

## Commands

```
vortex auth signup|login|logout|whoami
vortex orgs list|create <name>
vortex projects list|create <name>            # within the current org
vortex apps list|create <name>|status <id>|deploy <id>|logs <id>|restart <id>|stop <id>|destroy <id>
vortex services catalog|list|create <template-key>
vortex secrets list|set KEY=VALUE...|unset KEY...   (--app <id>)
vortex plans                                  # billing catalog
vortex config set-context|show
vortex version [--server]
```

### Examples

```bash
# Pick a working context
vortex orgs list
vortex config set-context --org org_123
vortex projects list

# Deploy an app
vortex apps create web --git https://github.com/me/web --branch main --cpu 0.5 --memory 512
vortex apps list
vortex apps deploy <app-id>
vortex apps logs <app-id>
vortex apps status <app-id>

# Secrets (app env vars)
vortex secrets set DATABASE_URL=postgres://... LOG_LEVEL=info --app <app-id>
vortex secrets list --app <app-id>
vortex secrets unset LOG_LEVEL --app <app-id>

# One-click services
vortex services catalog
vortex services create wordpress --name blog

# Billing / scripting
vortex plans
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
│   │   ├── apps.go services.go secrets.go plans.go config.go version.go
│   └── version/version.go      # build-stamped version
└── README.md
```

The API client maps exactly onto the control-plane routes
(`/v1/auth/*`, `/v1/orgs/{orgID}/...`, `/v1/services/catalog`,
`/v1/billing/plans`, `/v1/me`, `/v1/version`). The CLI never invents endpoints.

## Develop / test

```bash
cd apps/cli
go mod tidy        # resolve dependencies (cobra, golang.org/x/term, yaml.v3)
go build ./...
go vet ./...
go test ./...
```
