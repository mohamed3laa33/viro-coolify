#!/usr/bin/env bash
# Tear down Viro and (optionally) the DigitalOcean cluster + registry.
#
#   ./deploy/scripts/teardown.sh            # uninstall the Helm release only
#   VORTEX_DESTROY_CLUSTER=1 ./deploy/scripts/teardown.sh   # also delete cluster + registry
set -euo pipefail

# Canonical namespace/release/cluster/registry names default to "vortex" and match
# 01-provision-doks.sh, 02-build-and-push.sh, 03-deploy.sh and deploy/helmfile.yaml.
NAMESPACE="${VORTEX_NAMESPACE:-vortex}"
RELEASE="${VORTEX_RELEASE:-vortex}"
CLUSTER_NAME="${VORTEX_CLUSTER_NAME:-vortex}"
REGISTRY_NAME="${VORTEX_REGISTRY_NAME:-vortex}"

echo "==> Uninstalling Helm release '${RELEASE}'"
helm uninstall "${RELEASE}" --namespace "${NAMESPACE}" || true

if [[ "${VORTEX_DESTROY_CLUSTER:-0}" == "1" ]]; then
  echo "==> Deleting DOKS cluster '${CLUSTER_NAME}' and registry '${REGISTRY_NAME}'"
  doctl kubernetes cluster delete "${CLUSTER_NAME}" --force || true
  doctl registry delete "${REGISTRY_NAME}" --force || true
fi
echo "==> Done."
