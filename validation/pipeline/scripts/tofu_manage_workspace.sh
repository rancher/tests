#!/bin/bash
set -e

# OpenTofu workspace management helper
# Handles workspace creation, selection, and verification in a robust way.

SCRIPT_NAME="$(basename "$0")"
readonly SCRIPT_NAME
SCRIPT_DIR="$(dirname "$0")"
readonly SCRIPT_DIR
QA_INFRA_WORK_PATH="${QA_INFRA_WORK_PATH:-/root/go/src/github.com/rancher/qa-infra-automation}"
readonly QA_INFRA_WORK_PATH

# Logging functions will be provided by airgap_lib.sh

validate_prerequisites() {
  # If logging helper already exists, assume airgap library is loaded
  if type log_info >/dev/null 2>&1; then
    :
  else
    local lib_candidates=(
      "${SCRIPT_DIR}/airgap_lib.sh"
      "/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap_lib.sh"
      "/root/go/src/github.com/rancher/qa-infra-automation/validation/pipeline/scripts/airgap_lib.sh"
      "/root/qa-infra-automation/validation/pipeline/scripts/airgap_lib.sh"
    )

    for lib in "${lib_candidates[@]}"; do
      if [[ -f "$lib" ]]; then
        # shellcheck disable=SC1090
        source "$lib"
        log_info "Sourced airgap library from: $lib"
        break
      fi
    done

    if ! type log_info >/dev/null 2>&1; then
      log_error "airgap_lib.sh not found in expected locations: ${lib_candidates[*]}"
      exit 1
    fi
  fi

  command -v tofu >/dev/null 2>&1 || { log_error "tofu binary not found in PATH"; exit 1; }
  [[ -n "${TF_WORKSPACE:-}" ]] || log_warning "TF_WORKSPACE empty - ensure you set the target workspace"
}

manage_workspace() {
  local workspace_name="${1:-$TF_WORKSPACE}"
  [[ -n "$workspace_name" ]] || { log_error "Workspace name not provided"; exit 1; }

  log_info "Workspace management starting: $workspace_name"
  log_info "QA_INFRA_WORK_PATH=$QA_INFRA_WORK_PATH"

  # Export AWS credentials for OpenTofu (airgap compatibility)
  export AWS_REGION="${AWS_REGION:-us-east-2}"
  export AWS_DEFAULT_REGION="${AWS_REGION}"

  # Use a safe cd
  if ! cd "$QA_INFRA_WORK_PATH"; then
    log_error "Failed to change to QA repo path: $QA_INFRA_WORK_PATH"
    exit 1
  fi

  log_info "Listing current workspaces"
  tofu -chdir=tofu/aws/modules/airgap workspace list || log_warning "Failed to list workspaces"

  # Check if workspace exists
  log_info "Checking if workspace exists: $workspace_name"
  local workspace_exists
  workspace_exists=$(tofu -chdir=tofu/aws/modules/airgap workspace list 2>/dev/null | grep -w -- "$workspace_name" || true)

  if [[ -z "$workspace_exists" ]]; then
    log_info "Workspace $workspace_name does not exist - creating"
    # Temporarily unset TF_WORKSPACE to allow creation if necessary
    unset TF_WORKSPACE
    if ! tofu -chdir=tofu/aws/modules/airgap workspace new "$workspace_name"; then
      log_error "Failed to create workspace: $workspace_name"
      exit 1
    fi
    export TF_WORKSPACE="$workspace_name"
    log_info "Workspace $workspace_name created"
  else
    log_info "Workspace $workspace_name already exists"
    export TF_WORKSPACE="$workspace_name"
  fi

  # Verify selection
  log_info "Verifying workspace selection"
  local current_workspace
  current_workspace=$(tofu -chdir=tofu/aws/modules/airgap workspace show 2>/dev/null || echo "")
  log_info "Current workspace: $current_workspace"

  if [[ "$current_workspace" != "$workspace_name" ]]; then
    log_error "Expected workspace $workspace_name but got '$current_workspace'"
    log_info "Available workspaces:"
    tofu -chdir=tofu/aws/modules/airgap workspace list || true
    exit 1
  fi

  # Final confirmation
  if tofu -chdir=tofu/aws/modules/airgap workspace list | grep -q -- "$workspace_name"; then
    log_info "[OK] Workspace '$workspace_name' confirmed to exist"
    log_info "[OK] TF_WORKSPACE: ${TF_WORKSPACE}"
  else
    log_error "Workspace '$workspace_name' not found after creation/selection"
    tofu -chdir=tofu/aws/modules/airgap workspace list || true
    exit 1
  fi

  # Re-initialize to ensure workspace is properly configured (airgap compatibility)
  log_info "Re-initializing workspace to ensure proper configuration"
  tofu -chdir=tofu/aws/modules/airgap init -input=false -upgrade || log_warning "tofu init returned non-zero"
  log_info "Workspace management completed for: $workspace_name"
}

main() {
  validate_prerequisites
  manage_workspace "$@"
}

trap 'log_error "Script failed at line $LINENO"' ERR

main "$@"