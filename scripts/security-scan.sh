#!/usr/bin/env bash
# Local security scan — the same checks the (now manual-only) CI Security job runs.
# Usage: ./scripts/security-scan.sh
#
# Requires: trivy, gitleaks (install via `brew install trivy gitleaks` on macOS).
# Mirrors .github/workflows/ci.yml so findings match CI.
set -euo pipefail
cd "$(dirname "$0")/.."

fail=0

if command -v trivy >/dev/null 2>&1; then
  echo "==> Trivy: dependency & secret scan (HIGH/CRITICAL)"
  trivy fs --scanners vuln,secret --severity HIGH,CRITICAL --ignore-unfixed \
    --exit-code 1 --no-progress --skip-dirs _ref-volo,_ref-viro-app,_clone . || fail=1

  echo "==> Trivy: IaC / K8s misconfig scan (HIGH/CRITICAL)"
  # NOTE: `trivy config` does not support --no-progress in v0.71.x (it is fatal:
  # "unknown flag: --no-progress"). Use --quiet, which it does accept. Skip the
  # reference clones (_ref-volo/_ref-viro-app/_clone) too — same as `trivy fs` —
  # since their misconfigs are upstream prior-art, not Vortex deploy artifacts.
  trivy config --severity HIGH,CRITICAL --exit-code 1 --quiet \
    --skip-dirs deploy/charts/common-chart,_ref-volo,_ref-viro-app,_clone . || fail=1
else
  echo "!! trivy not installed — skipping (brew install trivy)"
fi

if command -v gitleaks >/dev/null 2>&1; then
  echo "==> Gitleaks: secret scan"
  gitleaks dir . --config .gitleaks.toml --no-banner --redact --exit-code 1 || fail=1
else
  echo "!! gitleaks not installed — skipping (brew install gitleaks)"
fi

if [ "$fail" -ne 0 ]; then
  echo "SECURITY SCAN: findings detected (see above)"; exit 1
fi
echo "SECURITY SCAN: clean"
