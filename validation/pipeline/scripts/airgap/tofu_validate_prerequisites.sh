#!/bin/bash
set -e

# OpenTofu prerequisites validation helper
# Validates environment, OpenTofu installation, and required files for airgap/destroy operations.

SCRIPT_NAME="$(basename "$0")"
readonly SCRIPT_NAME
SCRIPT_DIR="$(dirname "$0")"
readonly SCRIPT_DIR
QA_INFRA_WORK_PATH="${QA_INFRA_WORK_PATH:-/root/go/src/github.com/rancher/qa-infra-automation}"
readonly QA_INFRA_WORK_PATH
TERRAFORM_VARS_FILENAME="${TERRAFORM_VARS_FILENAME:-cluster.tfvars}"
readonly TERRAFORM_VARS_FILENAME

# Logging functions will be provided by airgap_lib.sh

validate_prerequisites() {
    # Ensure tofu binary is available or fail fast
    if ! command -v tofu >/dev/null 2>&1; then
        log_error "tofu binary not found in PATH"
        exit 1
    fi

    # If logging helpers from airgap_lib.sh are not available, try to source the library
    if ! type log_info >/dev/null 2>&1; then
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
    fi

    # Validate QA repo path
    if [[ ! -d "$QA_INFRA_WORK_PATH" ]]; then
        log_error "QA_INFRA_WORK_PATH directory not found: $QA_INFRA_WORK_PATH"
        exit 1
    fi

    # Check for terraform vars file in expected locations
    if [[ -f "${QA_INFRA_WORK_PATH}/tofu/aws/modules/airgap/${TERRAFORM_VARS_FILENAME}" ]]; then
        log_info "Found terraform vars file at: ${QA_INFRA_WORK_PATH}/tofu/aws/modules/airgap/${TERRAFORM_VARS_FILENAME}"
    elif [[ -f "/root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/${TERRAFORM_VARS_FILENAME}" ]]; then
        log_info "Found terraform vars file at: /root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/${TERRAFORM_VARS_FILENAME}"
    else
        log_error "terraform vars file not found in expected locations"
        log_error "Checked: ${QA_INFRA_WORK_PATH}/tofu/aws/modules/airgap/${TERRAFORM_VARS_FILENAME}"
        log_error "       /root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/${TERRAFORM_VARS_FILENAME}"
        exit 1
    fi

    log_info "Prerequisites validated successfully"
}

main() {
    validate_prerequisites

    log_info "Starting prerequisites validation: $SCRIPT_NAME"
    log_info "QA_INFRA_WORK_PATH=$QA_INFRA_WORK_PATH"
    log_info "TF_WORKSPACE=${TF_WORKSPACE:-<not-set>}"
    log_info "TERRAFORM_VARS_FILENAME=$TERRAFORM_VARS_FILENAME"

    # Diagnostic info (limited, safe)
    log_info "Current working directory: $(pwd)"
    log_info "Listing tofu module directory (if present):"
    if [[ -d "${QA_INFRA_WORK_PATH}/tofu/aws/modules/airgap" ]]; then
        ls -la "${QA_INFRA_WORK_PATH}/tofu/aws/modules/airgap/" | head -50 || true
    else
        log_warning "Module directory not found: ${QA_INFRA_WORK_PATH}/tofu/aws/modules/airgap"
    fi

    log_info "All infrastructure prerequisites validated successfully"
}

trap 'log_error "Script failed at line $LINENO"' ERR
main "$@"
