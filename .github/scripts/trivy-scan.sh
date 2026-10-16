#!/usr/bin/env bash
# Scans a `docker save` tarball with Trivy (no daemon access needed).
# Env: TARBALL TRIVY_IMAGE TRIVY_REPORT_SEVERITY (logged only)
#      TRIVY_FAIL_SEVERITY (fixable findings fail the scan)
set -euo pipefail

TAR="${TARBALL:?}"
DIR=$(cd "$(dirname "$TAR")" && pwd)

trivy() {
  docker run --rm \
    -v "$DIR:$DIR:ro" \
    -v "$HOME/.cache/trivy:/root/.cache/trivy" \
    "$TRIVY_IMAGE" "$@"
}

# Report: everything at TRIVY_REPORT_SEVERITY, never fails.
trivy image --input "$DIR/$(basename "$TAR")" --scanners vuln --no-progress \
  --severity "$TRIVY_REPORT_SEVERITY" --exit-code 0
# Gate: only fixable TRIVY_FAIL_SEVERITY findings fail; DB from the run above.
trivy image --input "$DIR/$(basename "$TAR")" --scanners vuln --no-progress \
  --skip-db-update --ignore-unfixed --severity "$TRIVY_FAIL_SEVERITY" --exit-code 1
