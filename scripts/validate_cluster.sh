#!/bin/bash
set -Eeuo pipefail
IFS=$'\n\t'

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
# Try to source common.sh for logging if available
if ! type log_info >/dev/null 2>&1; then
  for c in \
    "${SCRIPT_DIR}/lib/common.sh" \
    "/root/go/src/github.com/rancher/tests/scripts/lib/common.sh" \
    "/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/lib/common.sh"; do
    [ -f "$c" ] && . "$c" && break
  done
fi

log_info() { printf '[INFO] %s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"; }
log_warn() { printf '[WARN] %s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"; }
log_err()  { printf '[ERROR] %s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*" >&2; }

# KUBECONFIG should point to kubeconfig.yaml in shared volume when run inside container
: "${KUBECONFIG:=/source/kubeconfig.yaml}"
OUT_DIR="/source"
VALID_DIR="${OUT_DIR}/validation"
mkdir -p "$VALID_DIR" || true

command -v kubectl >/dev/null 2>&1 || { log_err "kubectl not found"; exit 0; }

log_info "Using KUBECONFIG=$KUBECONFIG"

# Basic cluster info
kubectl --kubeconfig "$KUBECONFIG" cluster-info >"$VALID_DIR/cluster-info.txt" 2>&1 || true
kubectl --kubeconfig "$KUBECONFIG" get nodes -o wide >"$VALID_DIR/nodes.txt" 2>&1 || true
kubectl --kubeconfig "$KUBECONFIG" get pods -A -o wide >"$VALID_DIR/pods.txt" 2>&1 || true
kubectl --kubeconfig "$KUBECONFIG" get events -A --sort-by=.lastTimestamp >"$VALID_DIR/events.txt" 2>&1 || true

# Wait for Rancher to be available if present
if kubectl --kubeconfig "$KUBECONFIG" get ns cattle-system >/dev/null 2>&1; then
  log_info "Waiting for Rancher deployment to be available"
  kubectl --kubeconfig "$KUBECONFIG" -n cattle-system rollout status deploy/rancher --timeout=10m >"$VALID_DIR/rancher-rollout.txt" 2>&1 || true
  kubectl --kubeconfig "$KUBECONFIG" -n cattle-system get deploy,svc,pods -o wide >"$VALID_DIR/rancher-resources.txt" 2>&1 || true
  # Collect logs from rancher pods (best-effort)
  for p in $(kubectl --kubeconfig "$KUBECONFIG" -n cattle-system get pods -l app=rancher -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
    kubectl --kubeconfig "$KUBECONFIG" -n cattle-system logs "$p" --tail=200 >"$VALID_DIR/${p}.log" 2>&1 || true
  done
fi

log_info "Validation artifacts written to $VALID_DIR"
