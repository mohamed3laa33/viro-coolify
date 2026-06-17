#!/usr/bin/env bash
# Build the Viro API + web images and push them to DigitalOcean Container Registry.
#
#   ./deploy/scripts/02-build-and-push.sh [tag]
set -euo pipefail

REGISTRY_NAME="${VORTEX_REGISTRY_NAME:-viro}"
REGISTRY="registry.digitalocean.com/${REGISTRY_NAME}"
TAG="${1:-$(git rev-parse --short HEAD 2>/dev/null || echo latest)}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

command -v doctl >/dev/null || { echo "doctl not found"; exit 1; }

echo "==> Logging in to DOCR"
doctl registry login

echo "==> Building API image -> ${REGISTRY}/vortex-api:${TAG}"
docker build -f "${ROOT}/docker/Dockerfile.api" \
  --build-arg VERSION="${TAG}" \
  --build-arg COMMIT="$(git -C "${ROOT}" rev-parse --short HEAD 2>/dev/null || echo unknown)" \
  -t "${REGISTRY}/vortex-api:${TAG}" "${ROOT}"

echo "==> Building web image -> ${REGISTRY}/vortex-web:${TAG}"
docker build -f "${ROOT}/docker/Dockerfile.web" -t "${REGISTRY}/vortex-web:${TAG}" "${ROOT}"

echo "==> Pushing"
docker push "${REGISTRY}/vortex-api:${TAG}"
docker push "${REGISTRY}/vortex-web:${TAG}"

echo "==> Pushed tag ${TAG}. Deploy with:"
echo "    ./deploy/scripts/03-deploy.sh ${TAG}"
