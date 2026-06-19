# Deploying Vortex to DigitalOcean

> **Prepared, not executed.** Nothing here creates cloud resources until *you* run it with
> your own DigitalOcean credentials. Local development needs none of this.

## One-shot install (recommended)

`deploy/install.sh` is the single entrypoint that stands up the **entire** platform end-to-end
with one command. It is **env-driven** and every step is **idempotent**, so re-running it converges
the cluster instead of failing.

```bash
# Fresh DigitalOcean account → fully-running platform:
VORTEX_JWT_SECRET=$(openssl rand -hex 32) \
VORTEX_SECRET_ENCRYPTION_KEY=$(openssl rand -hex 32) \
VORTEX_PG_PASSWORD=$(openssl rand -hex 24) \
VORTEX_DATABASE_URL="postgres://vortex:${VORTEX_PG_PASSWORD}@vortex-postgres.vortex.svc.cluster.local:5432/vortex?sslmode=require" \
VORTEX_DO_TOKEN=dop_v1_xxx \
  ./deploy/install.sh --yes
# or: make install INSTALL_ARGS="--yes"
```

It runs, in order (see `deploy/install.sh --help`):

1. **Prerequisite checks** — `kubectl`, `helm`, `helmfile` (and `terraform` + `doctl` unless
   `--skip-provision`); verifies cluster reachability (`kubectl cluster-info`); validates required
   env vars. It **refuses a weak/dev/empty `VORTEX_JWT_SECRET`**, mirroring the API's own
   production guard in `apps/api/internal/config/config.go`.
2. **Provision** (optional) — `terraform apply` in `deploy/terraform` to create the DOKS cluster +
   registry and write the kubeconfig. The DO token is passed via `TF_VAR_do_token` from
   `VORTEX_DO_TOKEN` (no secrets in tfvars). Skip with `--skip-provision` to use an existing cluster.
3. **Namespaces + secrets** — creates the canonical `vortex` namespace and the secrets the helmfile
   does *not* manage (`ghcr-pull` image-pull, `velero-cloud-credentials`, `external-dns-credentials`)
   from env via `kubectl … --dry-run=client -o yaml | kubectl apply -f -`, so re-runs are no-ops.
4. **Helmfile apply** — `helmfile -f deploy/helmfile.yaml apply` installs, in dependency order:
   cert-manager, Envoy Gateway (Gateway API), KEDA + keda-http-add-on, metrics-server,
   kube-prometheus-stack + Loki + promtail, Velero (only when a backup bucket is configured),
   external-dns, the `vortex-bootstrap` chart (in-cluster Postgres + shared Gateway + GatewayClass +
   DNS-01 issuer + wildcard TLS) and the **Vortex control plane** (api + web). When a private GHCR
   token is supplied it then wires `image.pullSecret=ghcr-pull` onto the control-plane release.
5. **Wait for rollouts** — `kubectl rollout status` for cert-manager, Envoy Gateway, the
   control-plane `vortex-viro-{api,web}` deployments, and the bootstrap Postgres StatefulSet.
6. **Print next steps** — discovers the **shared Gateway LoadBalancer** external IP and prints it,
   the **wildcard DNS records** to create (`*.<baseDomain>` plus the per-org / per-project wildcards
   for the 3-label tenant hosts), and the **dashboard URL**.

### Flags

| Flag | Effect |
|---|---|
| `--skip-provision` | Use the current kubeconfig / existing cluster; do **not** run terraform. |
| `--skip-addons` | Install only `vortex-bootstrap` + the control plane (cluster already has the add-ons). |
| `--dry-run` | Change nothing: run `terraform plan` and `helmfile template` and print what *would* happen. |
| `--yes`, `-y` | Non-interactive (no confirmation prompt). |
| `-h`, `--help` | Full usage + env-var reference. |

### Environment variables

**Required** (the control plane runs as `VORTEX_ENV=production` and refuses to boot without these):

| Var | Purpose |
|---|---|
| `VORTEX_JWT_SECRET` | Strong JWT signing secret. The installer rejects the dev default / short values. |
| `VORTEX_SECRET_ENCRYPTION_KEY` | App-secret encryption key (otherwise secrets would be stored plaintext). |
| `VORTEX_PG_PASSWORD` | In-cluster Postgres password (no insecure default). |
| `VORTEX_DATABASE_URL` | Postgres DSN, `sslmode=require`. |
| `VORTEX_DO_TOKEN` | DigitalOcean token for cert-manager DNS-01 wildcard TLS; also used by terraform unless `--skip-provision`. |

**Optional:**

| Var | Default | Purpose |
|---|---|---|
| `VORTEX_BASE_DOMAIN` | `vortex.v60ai.com` | Apex domain. Tenant hosts are `<app>.<project>.<org>.<base>`. |
| `VORTEX_NAMESPACE` | `vortex` | Control-plane namespace. |
| `VORTEX_RELEASE` | `vortex` | Control-plane Helm release name. |
| `VORTEX_IMAGE_REGISTRY` | DOCR | Registry hosting the `vortex-api` / `vortex-web` images. |
| `VORTEX_IMAGE_TAG` | `latest` | Image tag. |
| `VORTEX_ADMIN_EMAILS` | — | Comma-separated admin emails (admin-panel access). |
| `VORTEX_STRIPE_SECRET_KEY` / `VORTEX_STRIPE_WEBHOOK_SECRET` | — | Stripe creds; billing is enabled when the secret key is set. |
| `GHCR_PULL_TOKEN` | — | PAT (`read:packages`) for a **private** GHCR registry → creates the `ghcr-pull` image-pull secret. |
| `VORTEX_GHCR_USER` | derived | GHCR username for the pull secret (derived from a `ghcr.io/<owner>` registry when unset). |
| `VORTEX_VELERO_BUCKET` / `VORTEX_VELERO_REGION` / `VORTEX_VELERO_S3_URL` | — | Object store for **Velero backups**; setting the bucket enables the Velero release. |
| `VORTEX_VELERO_CREDENTIALS_FILE` | — | Path to an AWS-style INI creds file → the `velero-cloud-credentials` secret. |
| `VORTEX_EXTERNAL_DNS_ENABLED` | `false` | `true` to provision **external-dns** (auto-publishes the wildcard records). |
| `VORTEX_EXTERNAL_DNS_ZONES` | — | Comma-separated managed DNS zones. |
| `VORTEX_EXTERNAL_DNS_DO_TOKEN` | `VORTEX_DO_TOKEN` | DO token for external-dns. |
| `VORTEX_DNS_RECORD_TTL` | `300` | DNS record TTL (seconds). |

> The installer is the operator-facing complement to the CD pipeline
> (`.github/workflows/deploy.yml`): same charts, same canonical `vortex` namespace, same
> Gateway-API routing. The manual `scripts/` + Terraform paths below remain for fine-grained
> control, but `install.sh` is the supported one-command flow.

## Architecture on DigitalOcean

```
                      ┌────────────── DOKS cluster ──────────────┐
  app.viro… ─┐        │  Envoy Gateway + cert-manager (TLS)       │
             ├─ Ingress ─▶ vortex-web (Next.js)                     │
  api.viro… ─┘        │   └─▶ vortex-api (Go control-plane) ─┐      │
                      └───────────────────────────────────┼──────┘
                                                           │
                          DigitalOcean Managed Postgres ◀──┘
                          Coolify (separate droplet/node) ◀── /api/v1
```

- **vortex-web** + **vortex-api** run on **DigitalOcean Kubernetes (DOKS)**, images in **DOCR**.
- **Postgres** is DigitalOcean Managed Postgres.
- **Coolify** runs on its own droplet (install via its official one-liner) and is reached by the
  control-plane over its API token.

## Two ways to provision

### A) Scripts (`doctl`)
```bash
doctl auth init                       # once, with your DO token
./deploy/scripts/01-provision-doks.sh # cluster + registry + Gateway API + cert-manager
./deploy/scripts/02-build-and-push.sh # build + push images to DOCR
VORTEX_JWT_SECRET=$(openssl rand -hex 32) \
VORTEX_SECRET_ENCRYPTION_KEY=$(openssl rand -hex 32) \
VORTEX_DATABASE_URL=postgres://... \
VORTEX_COOLIFY_BASE_URL=https://coolify.example.com \
VORTEX_COOLIFY_TOKEN=... \
  ./deploy/scripts/03-deploy.sh       # helm upgrade --install
```

### B) Terraform
```bash
cd deploy/terraform
cp terraform.tfvars.example terraform.tfvars   # add your DO token
terraform init && terraform apply
$(terraform output -raw kubeconfig_command)
# then build/push (script 02) and deploy (script 03)
```

## Helm

The chart lives in `deploy/helm/viro`. Key values (`values.yaml`):

| Value | Purpose |
|---|---|
| `image.registry`, `image.tag` | DOCR registry + image tag |
| `secrets.*` | JWT secret, secret-encryption key, DB URL, Coolify + Stripe creds (pass via `--set`, never commit) |
| `ingress.apiHost`, `ingress.webHost` | public hostnames |
| `api.replicas`, `web.replicas` | scaling |
| `postgres.enabled` | in-cluster PG for testing (prod uses managed) |

```bash
helm upgrade --install viro deploy/helm/viro -n viro --create-namespace \
  --set image.tag=$(git rev-parse --short HEAD) \
  --set secrets.jwtSecret=... --set secrets.secretEncryptionKey=... --set secrets.databaseUrl=...
```

## DNS + TLS
Point `app.viro.example.com` and `api.viro.example.com` at the shared Gateway LoadBalancer IP
(`kubectl -n envoy-gateway-system get svc`). cert-manager + the `vortex-letsencrypt` ClusterIssuer
issue TLS automatically (create the issuer once).

## Teardown
```bash
./deploy/scripts/teardown.sh                     # remove the app
VORTEX_DESTROY_CLUSTER=1 ./deploy/scripts/teardown.sh   # also destroy cluster + registry
```
