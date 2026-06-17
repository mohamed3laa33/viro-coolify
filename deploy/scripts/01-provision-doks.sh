#!/usr/bin/env bash
# Provision DigitalOcean infrastructure for Viro: a DOKS cluster + container registry.
# Prepared for tomorrow — requires `doctl auth init` with your DO token first.
#
#   ./deploy/scripts/01-provision-doks.sh
#
# Configure via env vars (defaults shown):
set -euo pipefail

CLUSTER_NAME="${VIRO_CLUSTER_NAME:-viro}"
REGION="${VIRO_REGION:-fra1}"
NODE_SIZE="${VIRO_NODE_SIZE:-s-2vcpu-4gb}"
NODE_COUNT="${VIRO_NODE_COUNT:-2}"
K8S_VERSION="${VIRO_K8S_VERSION:-latest}"
REGISTRY_NAME="${VIRO_REGISTRY_NAME:-viro}"

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

echo "==> Wiring kubeconfig + registry credentials into the cluster"
doctl kubernetes cluster kubeconfig save "${CLUSTER_NAME}"
doctl registry kubernetes-manifest | kubectl apply -f -

echo "==> Installing ingress-nginx + cert-manager (idempotent)"
kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/controller-v1.11.3/deploy/static/provider/do/deploy.yaml
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.16.2/cert-manager.yaml

echo "==> Done. Next: ./deploy/scripts/02-build-and-push.sh"
