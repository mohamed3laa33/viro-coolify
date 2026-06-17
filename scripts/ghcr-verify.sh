#!/usr/bin/env bash
# Verify the Vortex images exist in GHCR and list their tags.
# Usage: GHCR_PAT=<token-with-read:packages> ./scripts/ghcr-verify.sh
set -euo pipefail
OWNER="${OWNER:-mohamed3laa33}"
: "${GHCR_PAT:?set GHCR_PAT to a PAT with read:packages}"
basic=$(printf '%s' "$GHCR_PAT" | base64 | tr -d '\n')
for img in vortex-api vortex-web; do
  token=$(curl -fsSL -H "Authorization: Basic $basic" \
    "https://ghcr.io/token?service=ghcr.io&scope=repository:$OWNER/$img:pull" \
    | sed -E 's/.*"token":"([^"]+)".*/\1/')
  echo "=== ghcr.io/$OWNER/$img ==="
  curl -fsSL -H "Authorization: Bearer $token" "https://ghcr.io/v2/$OWNER/$img/tags/list"
  echo
done
