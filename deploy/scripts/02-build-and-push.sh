#!/usr/bin/env bash
# Build the Viro API + web images and push them to DigitalOcean Container Registry.
#
#   ./deploy/scripts/02-build-and-push.sh [tag]
set -euo pipefail

REGISTRY_NAME="${VIRO_REGISTRY_NAME:-viro}"
REGISTRY="registry.digitalocean.com/${REGISTRY_NAME}"
TAG="${1:-$(git rev-parse --short HEAD 2>/dev/null || echo latest)}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

command -v doctl >/dev/null || { echo "doctl not found"; exit 1; }

echo "==> Logging in to DOCR"
doctl registry login

echo "==> Building API image -> ${REGISTRY}/viro-api:${TAG}"
docker build -f "${ROOT}/docker/Dockerfile.api" \
  --build-arg VERSION="${TAG}" \
  --build-arg COMMIT="$(git -C "${ROOT}" rev-parse --short HEAD 2>/dev/null || echo unknown)" \
  -t "${REGISTRY}/viro-api:${TAG}" "${ROOT}"

echo "==> Building web image -> ${REGISTRY}/viro-web:${TAG}"
docker build -f "${ROOT}/docker/Dockerfile.web" -t "${REGISTRY}/viro-web:${TAG}" "${ROOT}"

echo "==> Pushing"
docker push "${REGISTRY}/viro-api:${TAG}"
docker push "${REGISTRY}/viro-web:${TAG}"

echo "==> Pushed tag ${TAG}. Deploy with:"
echo "    ./deploy/scripts/03-deploy.sh ${TAG}"
