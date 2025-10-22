#!/bin/bash
set -e

# Minimal Rancher deployment wrapper
# Runs ansible-playbook with extra-vars only
# Replaces: ansible_deploy_rancher.sh

readonly SCRIPT_NAME="$(basename "$0")"
readonly QA_INFRA_CLONE_PATH="/root/qa-infra-automation"
readonly PLAYBOOK="$QA_INFRA_CLONE_PATH/ansible/rke2/airgap/playbooks/deploy/rancher-helm-deploy-playbook.yml"
readonly INVENTORY="/root/ansible/rke2/airgap/inventory.yml"
readonly GROUP_VARS="/root/ansible/rke2/airgap/group_vars/all.yml"

log_info() { echo "[INFO] $(date '+%Y-%m-%d %H:%M:%S') $*"; }
log_error() { echo "[ERROR] $(date '+%Y-%m-%d %H:%M:%S') $*" >&2; }

main() {
  local extra_vars=""

  log_info "Starting minimal Rancher deployment with $SCRIPT_NAME"

  # Validate prerequisites
  [[ -f "$PLAYBOOK" ]] || { log_error "Playbook not found: $PLAYBOOK"; exit 1; }
  [[ -f "$INVENTORY" ]] || { log_error "Inventory not found: $INVENTORY"; exit 1; }
  [[ -f "$GROUP_VARS" ]] || { log_error "Group vars not found: $GROUP_VARS"; exit 1; }
  command -v ansible-playbook >/dev/null || { log_error "ansible-playbook not found"; exit 1; }

  # Build extra-vars from environment
  [[ -n "${RANCHER_VERSION:-}" ]] && extra_vars+=" -e rancher_version=${RANCHER_VERSION}"
  [[ -n "${HOSTNAME_PREFIX:-}" ]] && extra_vars+=" -e hostname_prefix=${HOSTNAME_PREFIX}"
  [[ -n "${RANCHER_HOSTNAME:-}" ]] && extra_vars+=" -e rancher_hostname=${RANCHER_HOSTNAME}"

  cd "$QA_INFRA_CLONE_PATH/ansible/rke2/airgap" || exit 1

  log_info "Running: ansible-playbook -i $INVENTORY $PLAYBOOK -v $extra_vars"
  ansible-playbook -i "$INVENTORY" "$PLAYBOOK" -v $extra_vars

  log_info "Rancher deployment completed"
}

main "$@"