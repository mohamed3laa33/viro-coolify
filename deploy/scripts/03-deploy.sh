#!/usr/bin/env bash
# Deploy (or upgrade) Viro on the DOKS cluster via Helm.
#
#   ./deploy/scripts/03-deploy.sh [tag]
#
# Secrets are passed via env vars and NEVER committed:
#   VIRO_JWT_SECRET, VIRO_DATABASE_URL, VIRO_COOLIFY_BASE_URL, VIRO_COOLIFY_TOKEN,
#   VIRO_STRIPE_SECRET_KEY, VIRO_STRIPE_WEBHOOK_SECRET
set -euo pipefail

NAMESPACE="${VIRO_NAMESPACE:-viro}"
RELEASE="${VIRO_RELEASE:-viro}"
REGISTRY_NAME="${VIRO_REGISTRY_NAME:-viro}"
TAG="${1:-$(git rev-parse --short HEAD 2>/dev/null || echo latest)}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHART="${ROOT}/deploy/helm/viro"

command -v helm >/dev/null || { echo "helm not found"; exit 1; }

echo "==> helm upgrade --install ${RELEASE} (tag ${TAG}) into ns/${NAMESPACE}"
helm upgrade --install "${RELEASE}" "${CHART}" \
  --namespace "${NAMESPACE}" --create-namespace \
  --set image.registry="registry.digitalocean.com/${REGISTRY_NAME}" \
  --set image.tag="${TAG}" \
  --set secrets.jwtSecret="${VIRO_JWT_SECRET:?set VIRO_JWT_SECRET}" \
  --set secrets.databaseUrl="${VIRO_DATABASE_URL:?set VIRO_DATABASE_URL}" \
  --set secrets.coolifyBaseUrl="${VIRO_COOLIFY_BASE_URL:-}" \
  --set secrets.coolifyToken="${VIRO_COOLIFY_TOKEN:-}" \
  --set secrets.stripeSecretKey="${VIRO_STRIPE_SECRET_KEY:-}" \
  --set secrets.stripeWebhookSecret="${VIRO_STRIPE_WEBHOOK_SECRET:-}" \
  --wait --timeout 5m

echo "==> Rollout status"
kubectl -n "${NAMESPACE}" rollout status deploy/"${RELEASE}-viro-api"
kubectl -n "${NAMESPACE}" rollout status deploy/"${RELEASE}-viro-web"
echo "==> Done."
