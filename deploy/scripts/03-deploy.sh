#!/usr/bin/env bash
# Deploy (or upgrade) the Vortex control plane on the DOKS cluster via Helm.
#
#   ./deploy/scripts/03-deploy.sh [tag]
#
# This upgrades ONLY the control-plane chart. The platform prerequisites
# (cert-manager, Envoy Gateway, KEDA, metrics-server) and the vortex-bootstrap
# chart (Postgres + shared Gateway + wildcard TLS) are stood up by helmfile:
#   helmfile -f deploy/helmfile.yaml apply
# Run that once first (or rely on the CD workflow), then use this for fast
# control-plane redeploys.
#
# CANONICAL NAMESPACE = "vortex" (matches deploy/helmfile.yaml, the chart and the
# deploy/k8s manifests). The Helm release name defaults to "vortex"; with chart
# name "viro" the resulting deployments are vortex-viro-{api,web}.
#
# Secrets are passed via env vars and NEVER committed:
#   VORTEX_JWT_SECRET, VORTEX_SECRET_ENCRYPTION_KEY, VORTEX_DATABASE_URL,
#   VORTEX_STRIPE_SECRET_KEY, VORTEX_STRIPE_WEBHOOK_SECRET
set -euo pipefail

NAMESPACE="${VORTEX_NAMESPACE:-vortex}"
RELEASE="${VORTEX_RELEASE:-vortex}"
# Container registry hosting the vortex-api / vortex-web images. Default is the
# DigitalOcean Container Registry path used by 02-build-and-push.sh; override with
# VORTEX_IMAGE_REGISTRY (e.g. ghcr.io/<owner>) for other registries.
REGISTRY_NAME="${VORTEX_REGISTRY_NAME:-vortex}"
IMAGE_REGISTRY="${VORTEX_IMAGE_REGISTRY:-registry.digitalocean.com/${REGISTRY_NAME}}"
BASE_DOMAIN="${VORTEX_BASE_DOMAIN:-vortex.v60ai.com}"
TAG="${1:-$(git rev-parse --short HEAD 2>/dev/null || echo latest)}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHART="${ROOT}/deploy/helm/viro"

command -v helm >/dev/null || { echo "helm not found"; exit 1; }

echo "==> helm upgrade --install ${RELEASE} (tag ${TAG}) into ns/${NAMESPACE}"
helm upgrade --install "${RELEASE}" "${CHART}" \
  --namespace "${NAMESPACE}" --create-namespace \
  --set image.registry="${IMAGE_REGISTRY}" \
  --set image.tag="${TAG}" \
  --set ingress.enabled=true \
  --set ingress.gatewayName=vortex \
  --set ingress.gatewayNamespace="${NAMESPACE}" \
  --set ingress.apiHost="api.${BASE_DOMAIN}" \
  --set ingress.webHost="app.${BASE_DOMAIN}" \
  --set web_public_api_url="https://api.${BASE_DOMAIN}" \
  --set secrets.jwtSecret="${VORTEX_JWT_SECRET:?set VORTEX_JWT_SECRET}" \
  --set secrets.secretEncryptionKey="${VORTEX_SECRET_ENCRYPTION_KEY:?set VORTEX_SECRET_ENCRYPTION_KEY}" \
  --set secrets.databaseUrl="${VORTEX_DATABASE_URL:?set VORTEX_DATABASE_URL}" \
  --set secrets.stripeSecretKey="${VORTEX_STRIPE_SECRET_KEY:-}" \
  --set secrets.stripeWebhookSecret="${VORTEX_STRIPE_WEBHOOK_SECRET:-}" \
  --wait --timeout 5m

echo "==> Rollout status"
# Chart name is "viro" => deployments are <release>-viro-{api,web}.
kubectl -n "${NAMESPACE}" rollout status deploy/"${RELEASE}-viro-api" --timeout=3m
kubectl -n "${NAMESPACE}" rollout status deploy/"${RELEASE}-viro-web" --timeout=3m
echo "==> Done."
