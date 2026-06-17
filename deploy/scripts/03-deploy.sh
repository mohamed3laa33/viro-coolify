#!/usr/bin/env bash
# Deploy (or upgrade) Viro on the DOKS cluster via Helm.
#
#   ./deploy/scripts/03-deploy.sh [tag]
#
# Secrets are passed via env vars and NEVER committed:
#   VORTEX_JWT_SECRET, VORTEX_DATABASE_URL, VORTEX_COOLIFY_BASE_URL, VORTEX_COOLIFY_TOKEN,
#   VORTEX_STRIPE_SECRET_KEY, VORTEX_STRIPE_WEBHOOK_SECRET
set -euo pipefail

NAMESPACE="${VORTEX_NAMESPACE:-viro}"
RELEASE="${VORTEX_RELEASE:-viro}"
REGISTRY_NAME="${VORTEX_REGISTRY_NAME:-viro}"
TAG="${1:-$(git rev-parse --short HEAD 2>/dev/null || echo latest)}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHART="${ROOT}/deploy/helm/viro"

command -v helm >/dev/null || { echo "helm not found"; exit 1; }

echo "==> helm upgrade --install ${RELEASE} (tag ${TAG}) into ns/${NAMESPACE}"
helm upgrade --install "${RELEASE}" "${CHART}" \
  --namespace "${NAMESPACE}" --create-namespace \
  --set image.registry="registry.digitalocean.com/${REGISTRY_NAME}" \
  --set image.tag="${TAG}" \
  --set secrets.jwtSecret="${VORTEX_JWT_SECRET:?set VORTEX_JWT_SECRET}" \
  --set secrets.databaseUrl="${VORTEX_DATABASE_URL:?set VORTEX_DATABASE_URL}" \
  --set secrets.coolifyBaseUrl="${VORTEX_COOLIFY_BASE_URL:-}" \
  --set secrets.coolifyToken="${VORTEX_COOLIFY_TOKEN:-}" \
  --set secrets.stripeSecretKey="${VORTEX_STRIPE_SECRET_KEY:-}" \
  --set secrets.stripeWebhookSecret="${VORTEX_STRIPE_WEBHOOK_SECRET:-}" \
  --wait --timeout 5m

echo "==> Rollout status"
kubectl -n "${NAMESPACE}" rollout status deploy/"${RELEASE}-vortex-api"
kubectl -n "${NAMESPACE}" rollout status deploy/"${RELEASE}-vortex-web"
echo "==> Done."
