#!/usr/bin/env bash
# Tear down Viro and (optionally) the DigitalOcean cluster + registry.
#
#   ./deploy/scripts/teardown.sh            # uninstall the Helm release only
#   VIRO_DESTROY_CLUSTER=1 ./deploy/scripts/teardown.sh   # also delete cluster + registry
set -euo pipefail

NAMESPACE="${VIRO_NAMESPACE:-viro}"
RELEASE="${VIRO_RELEASE:-viro}"
CLUSTER_NAME="${VIRO_CLUSTER_NAME:-viro}"
REGISTRY_NAME="${VIRO_REGISTRY_NAME:-viro}"

echo "==> Uninstalling Helm release '${RELEASE}'"
helm uninstall "${RELEASE}" --namespace "${NAMESPACE}" || true

if [[ "${VIRO_DESTROY_CLUSTER:-0}" == "1" ]]; then
  echo "==> Deleting DOKS cluster '${CLUSTER_NAME}' and registry '${REGISTRY_NAME}'"
  doctl kubernetes cluster delete "${CLUSTER_NAME}" --force || true
  doctl registry delete "${REGISTRY_NAME}" --force || true
fi
echo "==> Done."
