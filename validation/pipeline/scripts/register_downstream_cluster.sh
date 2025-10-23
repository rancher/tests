#!/bin/bash
set -e

# Register Downstream Cluster Script
# This script registers a downstream cluster with Rancher

# Standard script metadata
readonly SCRIPT_NAME="$(basename "$0")"
readonly SCRIPT_DIR="$(dirname "$0")"
readonly QA_INFRA_CLONE_PATH="/root/qa-infra-automation"

# Load the airgap library (try multiple candidate locations)
# shellcheck disable=SC1090
if ! type log_info >/dev/null 2>&1; then
  lib_candidates=(
    "${SCRIPT_DIR}/airgap_lib.sh"
    "/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap_lib.sh"
    "/root/go/src/github.com/rancher/qa-infra-automation/validation/pipeline/scripts/airgap_lib.sh"
    "/root/qa-infra-automation/validation/pipeline/scripts/airgap_lib.sh"
  )

  for lib in "${lib_candidates[@]}"; do
    if [[ -f "$lib" ]]; then
      source "$lib"
      log_info "Sourced airgap library from: $lib"
      break
    fi
  done

  if ! type log_info >/dev/null 2>&1; then
    echo "[ERROR] airgap_lib.sh not found in expected locations: ${lib_candidates[*]}" >&2
    exit 1
  fi
fi

main() {
  log_info "Starting downstream cluster registration with $SCRIPT_NAME"

  # Change to qa-infra directory
  cd "$QA_INFRA_CLONE_PATH" || {
    log_error "Failed to change to QA_INFRA_CLONE_PATH: $QA_INFRA_CLONE_PATH"
    exit 1
  }

  local REPO_ROOT
  REPO_ROOT=$(pwd)

  : "${CONFIG_FILE:=/root/go/src/github.com/rancher/tests/validation/config.yaml}"
  : "${TFVARS_FILE:=cluster.tfvars}"
  : "${GENERATED_TFVARS_FILE:=$REPO_ROOT/ansible/rancher/default-ha/generated.tfvars}"

  log_info "Initializing Terraform for downstream cluster..."
  tofu -chdir="tofu/rancher/cluster" init

  log_info "Applying Terraform configuration for downstream cluster..."
  tofu -chdir="tofu/rancher/cluster" apply -auto-approve -var-file="$TFVARS_FILE" -var-file="$GENERATED_TFVARS_FILE"

  log_info "Getting downstream cluster name..."
  local DOWNSTREAM_CLUSTER_NAME
  DOWNSTREAM_CLUSTER_NAME=$(tofu -chdir="tofu/rancher/cluster" output -raw name)

  log_info "Updating config file with cluster name: $DOWNSTREAM_CLUSTER_NAME"
  yq e ".rancher.clusterName = \"$DOWNSTREAM_CLUSTER_NAME\"" -i "$CONFIG_FILE"

  log_success "Downstream cluster registration completed successfully"
}

# Error handling
trap 'log_error "Script failed at line $LINENO"' ERR

# Execute main function
main "$@"