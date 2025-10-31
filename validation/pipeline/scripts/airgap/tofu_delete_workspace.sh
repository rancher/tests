#!/bin/bash
set -e

# OpenTofu workspace deletion script for destroy operations
# This script safely deletes the specified workspace after infrastructure destruction

# =============================================================================
# CONSTANTS
# =============================================================================

readonly SCRIPT_NAME="$(basename "$0")"
readonly SCRIPT_DIR="$(dirname "$0")"

# =============================================================================
# LOGGING FUNCTIONS
# =============================================================================

# Logging functions will be provided by airgap_lib.sh

# =============================================================================
# PREREQUISITE VALIDATION
# =============================================================================

validate_prerequisites() {
    # If logging helper already exists, assume airgap library is loaded
    if ! type log_info >/dev/null 2>&1; then
        # Load airgap library with robust sourcing
        local lib_candidates=(
            "${SCRIPT_DIR}/airgap_lib.sh"
            "/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap/airgap_lib.sh"
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

    [[ -n "${QA_INFRA_WORK_PATH:-}" ]] || {
        log_error "QA_INFRA_WORK_PATH not set"
        exit 1
    }
    [[ -n "${TF_WORKSPACE:-}" ]] || {
        log_error "TF_WORKSPACE not set"
        exit 1
    }
    command -v tofu >/dev/null || {
        log_error "tofu not found"
        exit 1
    }
}

# =============================================================================
# MAIN FUNCTION
# =============================================================================

main() {
    # Validate prerequisites
    validate_prerequisites

    log_info "Starting workspace deletion with $SCRIPT_NAME"
    log_info "Target workspace: ${TF_WORKSPACE}"

    # Export AWS credentials for OpenTofu
    export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID}"
    export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY}"
    export AWS_REGION="${AWS_REGION:-us-east-2}"
    export AWS_DEFAULT_REGION="${AWS_REGION:-us-east-2}"

    cd "${QA_INFRA_WORK_PATH}" || {
        log_error "Failed to change to QA_INFRA_WORK_PATH: ${QA_INFRA_WORK_PATH}"
        exit 1
    }

    echo 'Current workspaces before deletion:'
    tofu -chdir=tofu/aws/modules/airgap workspace list

    echo "Target workspace for deletion: ${TF_WORKSPACE}"

    # Validate that TF_WORKSPACE is set and not empty
    if [ -z "${TF_WORKSPACE}" ]; then
        echo "ERROR: TF_WORKSPACE environment variable is not set or is empty"
        echo "Available workspaces:"
        tofu -chdir=tofu/aws/modules/airgap workspace list
        exit 1
    fi

    # Check if workspace exists before attempting deletion
    echo "Checking if workspace '${TF_WORKSPACE}' exists..."
    WORKSPACE_EXISTS=$(tofu -chdir=tofu/aws/modules/airgap workspace list | grep -w "${TF_WORKSPACE}" || true)

    if [ -z "$WORKSPACE_EXISTS" ]; then
        echo "[INFO] Workspace '${TF_WORKSPACE}' does not exist - nothing to delete"
        echo "Available workspaces:"
        tofu -chdir=tofu/aws/modules/airgap workspace list
        exit 0
    fi

    # Cannot delete the currently selected workspace, so switch to default first
    echo "Checking current workspace..."
    CURRENT_WORKSPACE=$(tofu -chdir=tofu/aws/modules/airgap workspace show)
    echo "Current workspace: $CURRENT_WORKSPACE"

    # Store the target workspace name before potentially unsetting TF_WORKSPACE
    TARGET_WORKSPACE="${TF_WORKSPACE}"

    if [ "$CURRENT_WORKSPACE" = "${TF_WORKSPACE}" ]; then
        echo "Current workspace is the target workspace, switching to 'default'..."
        unset TF_WORKSPACE # Temporarily unset to allow workspace switch
        tofu -chdir=tofu/aws/modules/airgap workspace select default
        echo "Switched to default workspace"
    fi

    # Now delete the target workspace using the stored name
    echo "Deleting workspace '${TARGET_WORKSPACE}'..."
    tofu -chdir=tofu/aws/modules/airgap workspace delete -force "${TARGET_WORKSPACE}"

    # Verify deletion
    echo "Verifying workspace deletion..."
    WORKSPACE_STILL_EXISTS=$(tofu -chdir=tofu/aws/modules/airgap workspace list | grep -w "${TARGET_WORKSPACE}" || true)

    if [ -z "$WORKSPACE_STILL_EXISTS" ]; then
        echo "[OK] Workspace '${TARGET_WORKSPACE}' deleted successfully"
    else
        echo "ERROR: Workspace '${TARGET_WORKSPACE}' still exists after deletion attempt"
        exit 1
    fi

    echo 'Final workspace list after deletion:'
    tofu -chdir=tofu/aws/modules/airgap workspace list

    log_info "Workspace deletion completed"
}

# Error handling
trap 'log_error "Script failed at line $LINENO"' ERR

# Execute main function
main "$@"
