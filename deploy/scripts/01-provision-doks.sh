#!/usr/bin/env bash
# Provision DigitalOcean infrastructure for Viro: a DOKS cluster + container registry.
# Prepared for tomorrow — requires `doctl auth init` with your DO token first.
#
#   ./deploy/scripts/01-provision-doks.sh
#
# Configure via env vars (defaults shown):
set -euo pipefail

CLUSTER_NAME="${VORTEX_CLUSTER_NAME:-vortex}"
REGION="${VORTEX_REGION:-fra1}"
NODE_SIZE="${VORTEX_NODE_SIZE:-s-2vcpu-4gb}"
NODE_COUNT="${VORTEX_NODE_COUNT:-2}"
K8S_VERSION="${VORTEX_K8S_VERSION:-latest}"
REGISTRY_NAME="${VORTEX_REGISTRY_NAME:-vortex}"

command -v doctl >/dev/null || { echo "doctl not found; install it first"; exit 1; }
doctl account get >/dev/null || { echo "Run 'doctl auth init' first"; exit 1; }

echo "==> Creating container registry '${REGISTRY_NAME}' (if absent)"
doctl registry get >/dev/null 2>&1 || doctl registry create "${REGISTRY_NAME}" --subscription-tier basic

echo "==> Creating DOKS cluster '${CLUSTER_NAME}' in ${REGION} (${NODE_COUNT} x ${NODE_SIZE})"
if ! doctl kubernetes cluster get "${CLUSTER_NAME}" >/dev/null 2>&1; then
  doctl kubernetes cluster create "${CLUSTER_NAME}" \
    --region "${REGION}" \
    --version "${K8S_VERSION}" \
    --node-pool "name=default;size=${NODE_SIZE};count=${NODE_COUNT};auto-scale=true;min-nodes=2;max-nodes=5" \
    --wait
else
  echo "    cluster already exists"
fi

echo "==> Saving kubeconfig"
doctl kubernetes cluster kubeconfig save "${CLUSTER_NAME}"

echo "==> Registry: enable the DOCR integration for this cluster"
doctl kubernetes cluster registry add "${CLUSTER_NAME}" 2>/dev/null || \
  echo "    (enable DOCR integration in the DO control panel if the above is unavailable)"

# Everything else — Gateway API + Envoy Gateway, cert-manager, KEDA, metrics-server,
# Postgres, the shared Gateway + wildcard TLS, and the Vortex control plane — is
# installed by helmfile in dependency order. NO manual kubectl. ingress-nginx is
# retired (k8s blog 2025-11-11) and intentionally unused; routing is the Gateway API.

echo "==> Done. Next: ./deploy/scripts/02-build-and-push.sh"
