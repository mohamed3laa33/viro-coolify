# Vortex on Kubernetes — workload runtime

Decisions (locked with the product owner):
- **Control plane on Docker.** `vortex-api` + `vortex-web` run via Docker / `docker-compose`
  (one droplet or a small VM) — simple to operate. See `docker/docker-compose.full.yml`.
- **All customer workloads on Kubernetes** (DOKS). No Coolify runtime. Apps, services,
  WordPress, and databases (Redis/Postgres/MySQL/Mongo) all run on **one cluster** for
  cost control + K8s scheduling/limits.
- **One generic Helm chart for everything:** `deploy/charts/common-chart` (vendored from the
  team's `cross-helm-charts-template`, `common-chart` v0.6.0). Vortex renders per-workload
  values and does `helm upgrade --install` into the tenant's namespace.
- **KEDA** for autoscaling (event-driven; scale-to-zero capable) — see `keda` block in the
  chart's `values.yaml` and `templates/keda-scaledobject.yaml`.

## Tenancy & isolation
- **Namespace per organization** (`viro-org-<id>`), with a `ResourceQuota` + `LimitRange`
  derived from the org's (DB-driven) plan, plus `NetworkPolicy` for isolation.
- A workload (app/service/db) = one Helm release of `common-chart` in that namespace.

## The cost lever — resource overcommit (CRITICAL)
For a workload the user requests at, say, **4 GB / 1 CPU** (the advertised/billed size),
Vortex sets the chart's `deployment.resources`:

```
requests = requested × overcommitFactor   (default 0.20)   → 0.8 GB / 0.2 CPU  (reserved, scheduled on)
limits   = requested                        (1.00)          → 4 GB   / 1 CPU    (burst ceiling)
```

So the scheduler bin-packs on the small `requests` (~5× density → ~5× lower node cost),
while tenants burst to their full size when the node has room (Burstable QoS).

- **CPU** is compressible → overcommit aggressively (factor ~0.15–0.20); contention only throttles.
- **Memory** is NOT compressible → use a safer factor (~0.30–0.40) + node autoscaling on memory
  pressure + pod priorities, or you risk OOM kills when many pods burst at once.
- Overcommit factors (separate CPU/mem, global default + per-plan override) are **admin/DB
  configurable — never hardcoded**.

## Catalog template → chart values mapping
The DB-backed catalog (WordPress, Ghost, Redis, Postgres, …) maps each template to chart values:

| Template kind | Chart shape | Notes |
|---|---|---|
| `app` (Git/image) | `deployment` (stateless) + `service` + `ingress` | image from build/registry; HTTPRoute/Ingress for domains |
| `service` (WordPress, Ghost, n8n) | `deployment` + `service` + `ingress` (+ `persistence` if needed) | image/ports from the template |
| `database` (Postgres, MySQL, Mongo, Redis) | `deployment.stateful: true` (StatefulSet) + `service-headless` + `persistence`/`volumeClaimTemplates` | PVC sizing from plan |

Per-workload values Vortex computes: image repo/tag, ports, `resources` (overcommit math above),
`keda` (triggers + min/max, scale-to-zero for idle web apps via the KEDA HTTP add-on),
`ingress`/`gateway` host (custom domains), `persistence` (DBs), env via `secret`/`config`.

## How Vortex provisions (the Go `KubernetesBackend` — next to implement)
The platform layer talks to a `DeployBackend` interface; the Kubernetes implementation:
1. Ensures the org namespace + `ResourceQuota`/`LimitRange` (from the plan).
2. Renders `common-chart` values for the workload (mapping above + overcommit).
3. `helm upgrade --install <release> deploy/charts/common-chart -n <ns> -f <values>`
   (via the Helm Go SDK or shelling out to `helm`).
4. Reads status/logs/metrics back via the K8s API (client-go) for the UI tabs.

## Edge routing — Gateway API, ONE LoadBalancer (cost)

> ⚠️ `ingress-nginx` (kubernetes/ingress-nginx) is **retired**
> (<https://kubernetes.io/blog/2025/11/11/ingress-nginx-retirement/>) — Vortex does **not**
> use it. Routing is the **Gateway API**.

- **One shared `Gateway`** (`deploy/k8s/gateway.yaml`, backed by **Envoy Gateway**) = exactly
  **one cloud LoadBalancer** for the whole platform. Its HTTPS listener allows routes from all
  namespaces.
- **Each app = one `HTTPRoute`** in its org-project namespace, attached to the shared Gateway
  via `parentRefs`, with `hostnames: [<app>.<project>.<org>.vortex.v60ai.com]`. Adding apps
  never adds LoadBalancers — cost stays flat.
- **Namespace per org-project** (`vortex-<org>-<project>`): admins can list orgs/projects as
  namespaces; tenants have no K8s access.
- **TLS** via cert-manager **DNS-01** (DigitalOcean) wildcard certs — per-org
  `*.<org>.vortex.v60ai.com`, managed by the control plane as orgs are created.

Cluster prerequisites (install once, all current/non-deprecated): **Gateway API CRDs + Envoy
Gateway**, cert-manager, **KEDA**, metrics-server, and optionally a Postgres operator
(CloudNativePG) for managed DBs. Installed by `deploy/scripts/01-provision-doks.sh`.

> Status: chart vendored + KEDA added + renders clean (`helm lint` passes). The Go
> `KubernetesBackend` and the namespace/quota controller are the next build step.
