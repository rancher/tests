#!/bin/bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLEANUP_SCRIPT="${SCRIPT_DIR}/airgap_infrastructure_cleanup.sh"

if [[ ! -f "${CLEANUP_SCRIPT}" ]]; then
  echo "[ERROR] Cleanup script not found: ${CLEANUP_SCRIPT}" >&2
  exit 1
fi

exec /bin/bash "${CLEANUP_SCRIPT}" "$@"
