#!/usr/bin/env bash
#
# install.sh — the single one-shot installer for the Vortex platform.
#
# Stands up the ENTIRE platform end-to-end on a Kubernetes cluster (DigitalOcean
# DOKS by default) with ONE command. It is env-driven and every step is
# idempotent, so re-running it converges the cluster rather than failing.
#
# Order of operations (each step is safe to re-run):
#   1. Prerequisite checks (tools, env vars, cluster reachability).
#   2. (optional) terraform apply in deploy/terraform — provision the DOKS
#      cluster + write kubeconfig. Skip with --skip-provision to use an existing
#      cluster / kubeconfig.
#   3. Create namespaces and the secrets the helmfile does NOT manage itself
#      (GHCR image-pull, velero-cloud-credentials, external-dns creds) from env,
#      using `kubectl apply` / `--dry-run=client -o yaml | apply` so re-runs are
#      no-ops instead of "already exists" errors.
#   4. `helmfile -f deploy/helmfile.yaml apply` — installs, in dependency order:
#      cert-manager, Envoy Gateway (Gateway API), KEDA + keda-http-add-on,
#      metrics-server, kube-prometheus-stack + loki + promtail, velero (only when
#      a backup bucket is configured), external-dns, the vortex-bootstrap chart
#      (in-cluster Postgres + shared Gateway + GatewayClass + DNS-01 issuer +
#      wildcard TLS) and the Vortex control plane (api + web).
#   5. Wait for the key rollouts (kubectl rollout status).
#   6. Discover the shared Gateway LoadBalancer external IP and print it, the
#      wildcard DNS records the operator must create, and the dashboard URL.
#
# This is the operator-facing complement to the CD pipeline
# (.github/workflows/deploy.yml): same charts, same canonical "vortex"
# namespace, same Gateway-API routing — no manual kubectl in the deploy path
# beyond creating env-sourced secrets the helmfile cannot template safely.
#
# Usage: deploy/install.sh [flags]   (run --help for the full list).
#
set -euo pipefail

# ---------------------------------------------------------------------------
# Paths
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
HELMFILE="${SCRIPT_DIR}/helmfile.yaml"
TERRAFORM_DIR="${SCRIPT_DIR}/terraform"

# ---------------------------------------------------------------------------
# Flags (defaults)
# ---------------------------------------------------------------------------
SKIP_PROVISION=0
SKIP_ADDONS=0
DRY_RUN=0
ASSUME_YES=0

# ---------------------------------------------------------------------------
# Config (env-driven; see usage() for the full table)
# ---------------------------------------------------------------------------
# Required (control plane refuses to boot without these in production):
#   VORTEX_JWT_SECRET            strong JWT signing secret (NOT the dev default)
#   VORTEX_SECRET_ENCRYPTION_KEY app-secret encryption key
#   VORTEX_PG_PASSWORD           in-cluster Postgres password
#   VORTEX_DATABASE_URL          Postgres DSN (sslmode=require)
#   VORTEX_DO_TOKEN              DigitalOcean token for cert-manager DNS-01
# Required for provisioning (unless --skip-provision):
#   VORTEX_DO_TOKEN              reused as the terraform `do_token`
# Optional:
#   VORTEX_BASE_DOMAIN           apex domain (default vortex.v60ai.com)
#   VORTEX_NAMESPACE             control-plane namespace (default vortex)
#   VORTEX_RELEASE               control-plane Helm release (default vortex)
#   VORTEX_IMAGE_REGISTRY        image registry for api/web images
#   VORTEX_IMAGE_TAG             image tag (default latest)
#   VORTEX_ADMIN_EMAILS          comma list of admin emails
#   VORTEX_STRIPE_SECRET_KEY / VORTEX_STRIPE_WEBHOOK_SECRET
#   GHCR_PULL_TOKEN              PAT (read:packages) for a private GHCR registry
#   VORTEX_GHCR_USER             GHCR username (default: derived from registry)
#   VORTEX_VELERO_BUCKET / VORTEX_VELERO_REGION / VORTEX_VELERO_S3_URL
#   VORTEX_VELERO_CREDENTIALS_FILE  path to an AWS-style INI creds file for Velero
#   VORTEX_EXTERNAL_DNS_ENABLED     "true" to wire external-dns
#   VORTEX_EXTERNAL_DNS_ZONES       comma list of managed zones
#   VORTEX_EXTERNAL_DNS_DO_TOKEN    DO token for external-dns (default VORTEX_DO_TOKEN)
#   VORTEX_DNS_RECORD_TTL           record TTL seconds (default 300)
BASE_DOMAIN="${VORTEX_BASE_DOMAIN:-vortex.v60ai.com}"
NAMESPACE="${VORTEX_NAMESPACE:-vortex}"
RELEASE="${VORTEX_RELEASE:-vortex}"
# Image registry/tag are read directly from the environment by the helmfile
# (VORTEX_IMAGE_REGISTRY / VORTEX_IMAGE_TAG); no local copies needed here.
# Mirror the API's own production guard (apps/api/internal/config/config.go).
DEFAULT_DEV_JWT_SECRET="dev-insecure-secret-change-me"
# Canonical add-on namespaces (must match deploy/helmfile.yaml).
VELERO_NS="velero"
EXTERNAL_DNS_NS="external-dns"

# ---------------------------------------------------------------------------
# Logging helpers
# ---------------------------------------------------------------------------
log()  { printf '\033[1;35m==>\033[0m %s\n' "$*"; }
info() { printf '    %s\n' "$*"; }
warn() { printf '\033[1;33m[warn]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[error]\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
install.sh — one-shot installer for the Vortex platform.

USAGE:
  deploy/install.sh [flags]

FLAGS:
  --skip-provision   Use the current kubeconfig / existing cluster; do NOT run
                     terraform to create a DOKS cluster.
  --skip-addons      Install only vortex-bootstrap + the control plane; skip the
                     platform add-ons (cert-manager, Envoy Gateway, KEDA,
                     metrics-server, observability, velero, external-dns).
                     Use only on a cluster that already has them.
  --dry-run          Do not apply anything: run `terraform plan` and
                     `helmfile template` and print what WOULD be created.
  --yes, -y          Non-interactive: do not prompt for confirmation.
  -h, --help         Show this help and exit.

REQUIRED ENV VARS:
  VORTEX_JWT_SECRET             Strong JWT signing secret (must NOT be the dev
                               default; refused in production, mirroring the API).
  VORTEX_SECRET_ENCRYPTION_KEY  App-secret encryption key (refused empty in prod).
  VORTEX_PG_PASSWORD            In-cluster Postgres password.
  VORTEX_DATABASE_URL          Postgres DSN, e.g.
                               postgres://vortex:$PW@vortex-postgres.vortex.svc.cluster.local:5432/vortex?sslmode=require
  VORTEX_DO_TOKEN              DigitalOcean API token (cert-manager DNS-01 wildcard
                               TLS; also used by terraform unless --skip-provision).

OPTIONAL ENV VARS:
  VORTEX_BASE_DOMAIN           Apex domain (default: vortex.v60ai.com).
  VORTEX_NAMESPACE             Control-plane namespace (default: vortex).
  VORTEX_RELEASE               Control-plane Helm release name (default: vortex).
  VORTEX_IMAGE_REGISTRY        Registry hosting vortex-api/vortex-web images.
  VORTEX_IMAGE_TAG             Image tag (default: latest).
  VORTEX_ADMIN_EMAILS          Comma-separated admin emails (admin panel access).
  VORTEX_STRIPE_SECRET_KEY     Stripe secret key (enables billing when set).
  VORTEX_STRIPE_WEBHOOK_SECRET Stripe webhook signing secret.
  GHCR_PULL_TOKEN              PAT with read:packages for a private GHCR registry;
                               creates an in-cluster `ghcr-pull` image-pull secret.
  VORTEX_GHCR_USER             GHCR username for the pull secret (default: derived).
  VORTEX_VELERO_BUCKET         S3-compatible bucket; enables the Velero backup stack.
  VORTEX_VELERO_REGION         Velero bucket region.
  VORTEX_VELERO_S3_URL         Velero S3 endpoint (e.g. https://nyc3.digitaloceanspaces.com).
  VORTEX_VELERO_CREDENTIALS_FILE  AWS-style INI creds file for the velero-cloud-credentials secret.
  VORTEX_EXTERNAL_DNS_ENABLED  "true" to provision external-dns DNS automation.
  VORTEX_EXTERNAL_DNS_ZONES    Comma-separated managed DNS zones.
  VORTEX_EXTERNAL_DNS_DO_TOKEN DO token for external-dns (default: VORTEX_DO_TOKEN).
  VORTEX_DNS_RECORD_TTL        DNS record TTL seconds (default: 300).

EXAMPLES:
  # Full install on a fresh DigitalOcean account:
  VORTEX_JWT_SECRET=$(openssl rand -hex 32) \
  VORTEX_SECRET_ENCRYPTION_KEY=$(openssl rand -hex 32) \
  VORTEX_PG_PASSWORD=$(openssl rand -hex 24) \
  VORTEX_DATABASE_URL="postgres://vortex:$VORTEX_PG_PASSWORD@vortex-postgres.vortex.svc.cluster.local:5432/vortex?sslmode=require" \
  VORTEX_DO_TOKEN=dop_v1_xxx \
    deploy/install.sh --yes

  # Re-run against an existing cluster (idempotent), no provisioning:
  ...env... deploy/install.sh --skip-provision --yes

  # Preview without changing anything:
  ...env... deploy/install.sh --skip-provision --dry-run
EOF
}

# ---------------------------------------------------------------------------
# Arg parsing
# ---------------------------------------------------------------------------
parse_args() {
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --skip-provision) SKIP_PROVISION=1 ;;
      --skip-addons)    SKIP_ADDONS=1 ;;
      --dry-run)        DRY_RUN=1 ;;
      --yes|-y)         ASSUME_YES=1 ;;
      -h|--help)        usage; exit 0 ;;
      *) die "unknown flag: $1 (run --help)" ;;
    esac
    shift
  done
}

# ---------------------------------------------------------------------------
# Step 1 — prerequisites
# ---------------------------------------------------------------------------
require_tool() {
  command -v "$1" >/dev/null 2>&1 || die "required tool '$1' not found in PATH — install it and re-run"
}

require_env() {
  local name="$1"
  # Indirect expansion; tolerate unset under `set -u`.
  local val="${!name:-}"
  [ -n "${val}" ] || die "required env var ${name} is not set (run --help for the full list)"
}

check_prereqs() {
  log "Checking prerequisites"

  require_tool kubectl
  require_tool helm
  require_tool helmfile

  if [ "${SKIP_PROVISION}" -eq 0 ]; then
    require_tool terraform
    require_tool doctl
    info "terraform + doctl present (provisioning enabled)"
  else
    info "skipping provisioning — using the current kubeconfig"
  fi

  # Required control-plane secrets / config.
  require_env VORTEX_JWT_SECRET
  require_env VORTEX_SECRET_ENCRYPTION_KEY
  require_env VORTEX_PG_PASSWORD
  require_env VORTEX_DATABASE_URL
  require_env VORTEX_DO_TOKEN

  # Mirror the API's production guard: refuse the dev/default or an empty JWT
  # secret. The control plane runs with VORTEX_ENV=production (set by helmfile),
  # so a weak secret would be rejected at boot — fail fast here instead.
  if [ "${VORTEX_JWT_SECRET}" = "${DEFAULT_DEV_JWT_SECRET}" ]; then
    die "VORTEX_JWT_SECRET is the insecure dev default — set a strong value (e.g. openssl rand -hex 32)"
  fi
  if [ "${#VORTEX_JWT_SECRET}" -lt 16 ]; then
    die "VORTEX_JWT_SECRET is too short (<16 chars) — use a strong value (e.g. openssl rand -hex 32)"
  fi

  info "config validated for base domain '${BASE_DOMAIN}', namespace '${NAMESPACE}'"

  # Cluster reachability is verified AFTER provisioning (terraform may create it).
  if [ "${SKIP_PROVISION}" -eq 1 ]; then
    verify_cluster_reachable
  fi
}

verify_cluster_reachable() {
  log "Verifying cluster reachability"
  if ! kubectl cluster-info >/dev/null 2>&1; then
    die "cannot reach a Kubernetes cluster (kubectl cluster-info failed) — set KUBECONFIG / current-context, or drop --skip-provision to provision one"
  fi
  local ctx
  ctx="$(kubectl config current-context 2>/dev/null || echo unknown)"
  info "connected to context '${ctx}'"
}

# ---------------------------------------------------------------------------
# Confirmation
# ---------------------------------------------------------------------------
confirm() {
  [ "${ASSUME_YES}" -eq 1 ] && return 0
  [ "${DRY_RUN}" -eq 1 ] && return 0
  printf '\nThis will install/upgrade the Vortex platform on the target cluster.\nContinue? [y/N] '
  local reply
  read -r reply
  case "${reply}" in
    y|Y|yes|YES) return 0 ;;
    *) die "aborted by user" ;;
  esac
}

# ---------------------------------------------------------------------------
# Step 2 — provision (terraform)
# ---------------------------------------------------------------------------
provision_cluster() {
  if [ "${SKIP_PROVISION}" -eq 1 ]; then
    return 0
  fi

  log "Provisioning DigitalOcean infrastructure (terraform)"
  [ -d "${TERRAFORM_DIR}" ] || die "terraform dir not found: ${TERRAFORM_DIR}"

  # terraform reads the DO token from TF_VAR_do_token (sourced from VORTEX_DO_TOKEN)
  # so the operator does not have to maintain a tfvars file with secrets in it.
  export TF_VAR_do_token="${VORTEX_DO_TOKEN}"
  [ -n "${VORTEX_BASE_DOMAIN:-}" ] || true

  terraform -chdir="${TERRAFORM_DIR}" init -input=false

  if [ "${DRY_RUN}" -eq 1 ]; then
    info "--dry-run: terraform plan (no changes applied)"
    terraform -chdir="${TERRAFORM_DIR}" plan -input=false
    return 0
  fi

  terraform -chdir="${TERRAFORM_DIR}" apply -input=false -auto-approve

  # Write/refresh the kubeconfig for the freshly-provisioned cluster.
  log "Saving kubeconfig for the provisioned cluster"
  local kube_cmd
  if kube_cmd="$(terraform -chdir="${TERRAFORM_DIR}" output -raw kubeconfig_command 2>/dev/null)" && [ -n "${kube_cmd}" ]; then
    info "${kube_cmd}"
    # shellcheck disable=SC2086  # the output is a known doctl command line.
    eval "${kube_cmd}"
  else
    warn "terraform did not emit kubeconfig_command — ensure your kubeconfig points at the new cluster"
  fi

  verify_cluster_reachable
}

# ---------------------------------------------------------------------------
# Step 3 — namespaces + env-sourced secrets
# ---------------------------------------------------------------------------
# Apply a namespace idempotently (no-op if it already exists).
ensure_namespace() {
  local ns="$1"
  if [ "${DRY_RUN}" -eq 1 ]; then
    info "--dry-run: would ensure namespace '${ns}'"
    return 0
  fi
  kubectl create namespace "${ns}" --dry-run=client -o yaml | kubectl apply -f -
}

# GHCR image-pull secret. Only when a registry token is supplied (public images
# / DOCR integration need none). Matches the CD workflow's `ghcr-pull` convention.
ensure_ghcr_pull_secret() {
  local token="${GHCR_PULL_TOKEN:-}"
  [ -n "${token}" ] || { info "no GHCR_PULL_TOKEN — skipping image-pull secret (assuming public/DOCR images)"; return 0; }

  local server="ghcr.io"
  local user="${VORTEX_GHCR_USER:-}"
  if [ -z "${user}" ]; then
    # Derive owner from a ghcr.io/<owner>[/...] registry if present.
    case "${VORTEX_IMAGE_REGISTRY:-}" in
      ghcr.io/*) user="${VORTEX_IMAGE_REGISTRY#ghcr.io/}"; user="${user%%/*}" ;;
    esac
  fi
  [ -n "${user}" ] || { warn "GHCR_PULL_TOKEN set but no VORTEX_GHCR_USER (and registry is not ghcr.io/<owner>) — skipping pull secret"; return 0; }

  if [ "${DRY_RUN}" -eq 1 ]; then
    info "--dry-run: would create docker-registry secret 'ghcr-pull' in ns/${NAMESPACE} for ${server} (user ${user})"
    return 0
  fi
  log "Creating/refreshing image-pull secret 'ghcr-pull' in ns/${NAMESPACE}"
  kubectl -n "${NAMESPACE}" create secret docker-registry ghcr-pull \
    --docker-server="${server}" \
    --docker-username="${user}" \
    --docker-password="${token}" \
    --dry-run=client -o yaml | kubectl apply -f -
}

# Velero object-store credentials. The velero release is gated on
# VORTEX_VELERO_BUCKET (see helmfile), and the chart expects an existing
# `velero-cloud-credentials` Secret (key `cloud`, AWS-style INI).
ensure_velero_secret() {
  [ -n "${VORTEX_VELERO_BUCKET:-}" ] || { info "no VORTEX_VELERO_BUCKET — skipping Velero credentials (backups disabled)"; return 0; }

  local creds_file="${VORTEX_VELERO_CREDENTIALS_FILE:-}"
  if [ -z "${creds_file}" ]; then
    warn "VORTEX_VELERO_BUCKET set but VORTEX_VELERO_CREDENTIALS_FILE is empty — Velero will install without usable credentials; set the creds file to enable backups"
    return 0
  fi
  [ -f "${creds_file}" ] || die "VORTEX_VELERO_CREDENTIALS_FILE='${creds_file}' not found"

  ensure_namespace "${VELERO_NS}"
  if [ "${DRY_RUN}" -eq 1 ]; then
    info "--dry-run: would create secret 'velero-cloud-credentials' in ns/${VELERO_NS} from ${creds_file}"
    return 0
  fi
  log "Creating/refreshing Velero credentials secret in ns/${VELERO_NS}"
  kubectl -n "${VELERO_NS}" create secret generic velero-cloud-credentials \
    --from-file=cloud="${creds_file}" \
    --dry-run=client -o yaml | kubectl apply -f -
}

# external-dns credentials. Only when external-dns is enabled. Stores the DO
# token in the `external-dns` namespace under the conventional secret used by
# the external-dns DigitalOcean provider (DO_TOKEN key).
ensure_external_dns_secret() {
  case "${VORTEX_EXTERNAL_DNS_ENABLED:-}" in
    true|TRUE|1|yes|YES) ;;
    *) info "VORTEX_EXTERNAL_DNS_ENABLED not true — skipping external-dns credentials"; return 0 ;;
  esac

  local token="${VORTEX_EXTERNAL_DNS_DO_TOKEN:-${VORTEX_DO_TOKEN}}"
  [ -n "${token}" ] || { warn "external-dns enabled but no token (VORTEX_EXTERNAL_DNS_DO_TOKEN / VORTEX_DO_TOKEN) — skipping"; return 0; }

  ensure_namespace "${EXTERNAL_DNS_NS}"
  if [ "${DRY_RUN}" -eq 1 ]; then
    info "--dry-run: would create secret 'external-dns-do' in ns/${EXTERNAL_DNS_NS}"
    return 0
  fi
  # Name/key MUST match the helmfile external-dns release env (secretKeyRef
  # name: external-dns-do, key: token) and deploy/helm-values/external-dns.yaml.
  log "Creating/refreshing external-dns credentials secret in ns/${EXTERNAL_DNS_NS}"
  kubectl -n "${EXTERNAL_DNS_NS}" create secret generic external-dns-do \
    --from-literal=token="${token}" \
    --dry-run=client -o yaml | kubectl apply -f -
}

ensure_namespaces_and_secrets() {
  log "Ensuring namespaces and env-sourced secrets"
  ensure_namespace "${NAMESPACE}"
  ensure_ghcr_pull_secret
  ensure_velero_secret
  ensure_external_dns_secret
}

# ---------------------------------------------------------------------------
# Step 4 — helmfile (all add-ons + bootstrap + control plane)
# ---------------------------------------------------------------------------
run_helmfile() {
  log "Installing platform via helmfile"
  [ -f "${HELMFILE}" ] || die "helmfile not found: ${HELMFILE}"

  # The helmfile reads all of its config from the same VORTEX_* env vars this
  # script already validated, so it inherits them from the environment.
  local selector=()
  if [ "${SKIP_ADDONS}" -eq 1 ]; then
    info "--skip-addons: limiting to the Vortex-owned releases (vortex-bootstrap + control plane)"
    # helmfile has no exclusion selector for "the upstream add-ons", so restrict
    # to the two Vortex-owned releases by name instead.
    selector=(--selector 'name=vortex-bootstrap' --selector 'name=vortex')
  fi

  if [ "${DRY_RUN}" -eq 1 ]; then
    info "--dry-run: helmfile template (rendering manifests; nothing applied)"
    helmfile -f "${HELMFILE}" "${selector[@]}" template
    return 0
  fi

  helmfile -f "${HELMFILE}" "${selector[@]}" apply

  # When a private GHCR pull secret exists, wire it onto the control-plane
  # release. The helmfile does not set image.pullSecret (the CD pipeline injects
  # it via a targeted helm upgrade); do the same here so private images pull.
  maybe_wire_pull_secret
}

maybe_wire_pull_secret() {
  [ -n "${GHCR_PULL_TOKEN:-}" ] || return 0
  # Only meaningful if the ghcr-pull secret was actually created.
  if ! kubectl -n "${NAMESPACE}" get secret ghcr-pull >/dev/null 2>&1; then
    return 0
  fi
  log "Wiring image-pull secret onto the control-plane release"
  helm upgrade "${RELEASE}" "${SCRIPT_DIR}/helm/viro" \
    --namespace "${NAMESPACE}" \
    --reuse-values \
    --set image.pullSecret=ghcr-pull \
    --wait --timeout 5m
}

# ---------------------------------------------------------------------------
# Step 5 — wait for key rollouts
# ---------------------------------------------------------------------------
wait_for_rollouts() {
  if [ "${DRY_RUN}" -eq 1 ]; then
    info "--dry-run: skipping rollout waits"
    return 0
  fi
  log "Waiting for key rollouts"

  # Platform prerequisites first (best-effort — names match upstream charts).
  rollout deploy cert-manager cert-manager 5m
  rollout deploy envoy-gateway-system envoy-gateway 5m

  # Control plane (release "vortex" + chart "viro" => vortex-viro-{api,web}).
  rollout deploy "${NAMESPACE}" "${RELEASE}-viro-api" 5m
  rollout deploy "${NAMESPACE}" "${RELEASE}-viro-web" 5m

  # In-cluster Postgres is a StatefulSet from the bootstrap chart.
  rollout statefulset "${NAMESPACE}" vortex-postgres 5m
}

# rollout <kind> <namespace> <name> <timeout>; warns (does not fail) if the
# resource is absent (e.g. --skip-addons, or a name drift in an upstream chart).
rollout() {
  local kind="$1" ns="$2" name="$3" timeout="$4"
  if ! kubectl -n "${ns}" get "${kind}/${name}" >/dev/null 2>&1; then
    warn "${kind}/${name} not found in ns/${ns} — skipping rollout wait"
    return 0
  fi
  info "waiting for ${kind}/${name} in ns/${ns} (timeout ${timeout})"
  kubectl -n "${ns}" rollout status "${kind}/${name}" --timeout="${timeout}" \
    || warn "${kind}/${name} did not become ready within ${timeout} — check 'kubectl -n ${ns} describe ${kind}/${name}'"
}

# ---------------------------------------------------------------------------
# Step 6 — discover the Gateway LB + print DNS + dashboard URL
# ---------------------------------------------------------------------------
# The shared Gateway is an Envoy Gateway Gateway ("vortex" in ns "vortex"); Envoy
# Gateway materializes one LoadBalancer Service for it in envoy-gateway-system.
discover_gateway_lb() {
  local jsonpath_ip='{.items[0].status.loadBalancer.ingress[0].ip}'
  local jsonpath_host='{.items[0].status.loadBalancer.ingress[0].hostname}'
  local ip host
  ip="$(kubectl -n envoy-gateway-system get svc \
        -l gateway.envoyproxy.io/owning-gateway-name="${RELEASE}" \
        -o jsonpath="${jsonpath_ip}" 2>/dev/null || true)"
  host="$(kubectl -n envoy-gateway-system get svc \
        -l gateway.envoyproxy.io/owning-gateway-name="${RELEASE}" \
        -o jsonpath="${jsonpath_host}" 2>/dev/null || true)"
  if [ -z "${ip}" ] && [ -z "${host}" ]; then
    # Fall back to any LoadBalancer service in the Envoy data-plane namespace.
    ip="$(kubectl -n envoy-gateway-system get svc --field-selector spec.type=LoadBalancer \
          -o jsonpath="${jsonpath_ip}" 2>/dev/null || true)"
    host="$(kubectl -n envoy-gateway-system get svc --field-selector spec.type=LoadBalancer \
          -o jsonpath="${jsonpath_host}" 2>/dev/null || true)"
  fi
  printf '%s' "${ip:-${host}}"
}

print_summary() {
  if [ "${DRY_RUN}" -eq 1 ]; then
    log "--dry-run complete — nothing was applied"
    return 0
  fi

  log "Discovering shared Gateway LoadBalancer"
  local lb
  lb="$(discover_gateway_lb)"

  printf '\n'
  log "Vortex install complete"
  printf '\n'

  if [ -n "${lb}" ]; then
    info "Shared Gateway LoadBalancer: ${lb}"
  else
    warn "Gateway LoadBalancer address not assigned yet (DigitalOcean can take a few minutes)."
    info "Re-check with: kubectl -n envoy-gateway-system get svc -l gateway.envoyproxy.io/owning-gateway-name=${RELEASE}"
    lb="<GATEWAY_LB_IP>"
  fi

  cat <<EOF

  Create these DNS records (all pointing at the Gateway LoadBalancer above):

    Type   Name                              Value
    ----   ----                              -----
    A      api.${BASE_DOMAIN}                ${lb}
    A      app.${BASE_DOMAIN}                ${lb}
    A      *.${BASE_DOMAIN}                  ${lb}      (platform wildcard: one label)

  Tenant app hosts are <app>.<project>.<org>.${BASE_DOMAIN} — THREE labels deep.
  A wildcard matches exactly one label, so each org needs its own wildcard. The
  control plane adds the matching Gateway listeners + cert automatically when an
  org is created; you (or external-dns, if enabled) must publish the DNS:

    A      *.<org>.${BASE_DOMAIN}            ${lb}
    A      *.<project>.<org>.${BASE_DOMAIN}  ${lb}

  If a managed-zone provider is wired (VORTEX_EXTERNAL_DNS_ENABLED=true), these
  records are created automatically by external-dns.

  Dashboard:  https://app.${BASE_DOMAIN}
  API:        https://api.${BASE_DOMAIN}

  TLS is issued by cert-manager via DNS-01 (DigitalOcean); the first issuance can
  take a couple of minutes. Watch it with:
    kubectl -n ${NAMESPACE} get certificate

EOF
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
main() {
  parse_args "$@"

  log "Vortex installer (repo: ${REPO_ROOT})"
  [ "${DRY_RUN}" -eq 1 ] && info "DRY RUN — no changes will be applied"
  [ "${SKIP_PROVISION}" -eq 1 ] && info "provisioning: SKIPPED"
  [ "${SKIP_ADDONS}" -eq 1 ] && info "add-ons: SKIPPED (bootstrap + control plane only)"

  check_prereqs
  confirm

  provision_cluster
  # On a dry run with provisioning, the cluster may not exist; only touch the
  # cluster (namespaces/secrets/helmfile) when it is reachable.
  if [ "${DRY_RUN}" -eq 1 ] && [ "${SKIP_PROVISION}" -eq 0 ]; then
    info "--dry-run with provisioning: skipping cluster-side steps (no live cluster)"
    log "--dry-run complete"
    return 0
  fi

  ensure_namespaces_and_secrets
  run_helmfile
  wait_for_rollouts
  print_summary
}

main "$@"
