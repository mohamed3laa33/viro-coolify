# Deploying Viro to DigitalOcean

> **Prepared, not executed.** Nothing here creates cloud resources until *you* run it with
> your own DigitalOcean credentials. Local development needs none of this.

## Architecture on DigitalOcean

```
                      ┌────────────── DOKS cluster ──────────────┐
  app.viro… ─┐        │  ingress-nginx + cert-manager (TLS)       │
             ├─ Ingress ─▶ viro-web (Next.js)                     │
  api.viro… ─┘        │   └─▶ viro-api (Go control-plane) ─┐      │
                      └───────────────────────────────────┼──────┘
                                                           │
                          DigitalOcean Managed Postgres ◀──┘
                          Coolify (separate droplet/node) ◀── /api/v1
```

- **viro-web** + **viro-api** run on **DigitalOcean Kubernetes (DOKS)**, images in **DOCR**.
- **Postgres** is DigitalOcean Managed Postgres.
- **Coolify** runs on its own droplet (install via its official one-liner) and is reached by the
  control-plane over its API token.

## Two ways to provision

### A) Scripts (`doctl`)
```bash
doctl auth init                       # once, with your DO token
./deploy/scripts/01-provision-doks.sh # cluster + registry + ingress + cert-manager
./deploy/scripts/02-build-and-push.sh # build + push images to DOCR
VIRO_JWT_SECRET=$(openssl rand -hex 32) \
VIRO_DATABASE_URL=postgres://... \
VIRO_COOLIFY_BASE_URL=https://coolify.example.com \
VIRO_COOLIFY_TOKEN=... \
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
| `secrets.*` | JWT secret, DB URL, Coolify + Stripe creds (pass via `--set`, never commit) |
| `ingress.apiHost`, `ingress.webHost` | public hostnames |
| `api.replicas`, `web.replicas` | scaling |
| `postgres.enabled` | in-cluster PG for testing (prod uses managed) |

```bash
helm upgrade --install viro deploy/helm/viro -n viro --create-namespace \
  --set image.tag=$(git rev-parse --short HEAD) \
  --set secrets.jwtSecret=... --set secrets.databaseUrl=...
```

## DNS + TLS
Point `app.viro.example.com` and `api.viro.example.com` at the ingress LoadBalancer IP
(`kubectl -n ingress-nginx get svc`). cert-manager + the `letsencrypt-prod` ClusterIssuer
issue TLS automatically (create the issuer once).

## Teardown
```bash
./deploy/scripts/teardown.sh                     # remove the app
VIRO_DESTROY_CLUSTER=1 ./deploy/scripts/teardown.sh   # also destroy cluster + registry
```
