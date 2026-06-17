#!/usr/bin/env bash
# Provision DigitalOcean infrastructure for Viro: a DOKS cluster + container registry.
# Prepared for tomorrow — requires `doctl auth init` with your DO token first.
#
#   ./deploy/scripts/01-provision-doks.sh
#
# Configure via env vars (defaults shown):
set -euo pipefail

CLUSTER_NAME="${VORTEX_CLUSTER_NAME:-viro}"
REGION="${VORTEX_REGION:-fra1}"
NODE_SIZE="${VORTEX_NODE_SIZE:-s-2vcpu-4gb}"
NODE_COUNT="${VORTEX_NODE_COUNT:-2}"
K8S_VERSION="${VORTEX_K8S_VERSION:-latest}"
REGISTRY_NAME="${VORTEX_REGISTRY_NAME:-viro}"

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

# NOTE: ingress-nginx (kubernetes/ingress-nginx) is being RETIRED
# (https://kubernetes.io/blog/2025/11/11/ingress-nginx-retirement/) — do NOT use it.
# Vortex routes via the Gateway API: ONE shared Gateway (= one LoadBalancer) +
# per-app HTTPRoute. Default controller: Envoy Gateway (Gateway-API-native, CNCF).
GATEWAY_API_VERSION="${VORTEX_GATEWAY_API_VERSION:-v1.2.1}"
ENVOY_GATEWAY_VERSION="${VORTEX_ENVOY_GATEWAY_VERSION:-v1.2.4}"
CERT_MANAGER_VERSION="${VORTEX_CERT_MANAGER_VERSION:-v1.16.3}"
KEDA_VERSION="${VORTEX_KEDA_VERSION:-2.16.0}"

echo "==> Installing Gateway API CRDs (${GATEWAY_API_VERSION})"
kubectl apply -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}/standard-install.yaml"

echo "==> Installing Envoy Gateway (${ENVOY_GATEWAY_VERSION})"
helm upgrade --install envoy-gateway oci://docker.io/envoyproxy/gateway-helm \
  --version "${ENVOY_GATEWAY_VERSION}" -n envoy-gateway-system --create-namespace --wait

echo "==> Installing cert-manager (${CERT_MANAGER_VERSION}) — supports Gateway API"
kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"

echo "==> Installing KEDA (${KEDA_VERSION}) for event-driven autoscaling + scale-to-zero"
helm repo add kedacore https://kedacore.github.io/charts >/dev/null 2>&1 || true
helm repo update >/dev/null
helm upgrade --install keda kedacore/keda --version "${KEDA_VERSION}" \
  -n keda --create-namespace --wait

echo "==> Installing metrics-server (for HPA/KEDA cpu/mem + the metrics UI)"
helm repo add metrics-server https://kubernetes-sigs.github.io/metrics-server/ >/dev/null 2>&1 || true
helm repo update >/dev/null
helm upgrade --install metrics-server metrics-server/metrics-server \
  -n kube-system --set 'args={--kubelet-insecure-tls}' --wait || true

echo "==> Applying the single shared Gateway (one LoadBalancer) + wildcard TLS"
echo "    (edit deploy/k8s/gateway.yaml for your domain + DNS-01 issuer first)"
kubectl apply -f "$(cd "$(dirname "${BASH_SOURCE[0]}")/../k8s" && pwd)/gateway.yaml" || \
  echo "    skipped — configure deploy/k8s/gateway.yaml then: kubectl apply -f deploy/k8s/gateway.yaml"

echo "==> Done. Next: ./deploy/scripts/02-build-and-push.sh"
