#!/bin/bash
set -e

# Consolidated OpenTofu initialization script for both airgap and destroy operations
# Supports both environment file sourcing and direct environment variable passing

# =============================================================================
# CONSTANTS
# =============================================================================

SCRIPT_NAME="$(basename "$0")"
readonly SCRIPT_NAME
SCRIPT_DIR="$(dirname "$0")"
readonly SCRIPT_DIR
ENV_FILE="/tmp/.env"
readonly ENV_FILE

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
    [[ -d "${QA_INFRA_WORK_PATH}" ]] || {
        log_error "QA_INFRA_WORK_PATH directory not found"
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

    log_info "Starting OpenTofu initialization with $SCRIPT_NAME"

    # Source environment file if it exists (airgap compatibility)
    if [[ -f "$ENV_FILE" ]]; then
        log_info "Sourcing environment file: $ENV_FILE"
        source "$ENV_FILE"

        # Export the sourced variables explicitly to ensure they're available
        export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID}"
        export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY}"
        export AWS_REGION="${AWS_REGION:-us-east-2}"
        export AWS_DEFAULT_REGION="${AWS_REGION:-us-east-2}"
        export S3_BUCKET_NAME="${S3_BUCKET_NAME}"
        export S3_BUCKET_REGION="${S3_BUCKET_REGION}"
        export S3_KEY_PREFIX="${S3_KEY_PREFIX}"
    else
        log_info "Environment file not found at $ENV_FILE, using Docker environment variables"
        # Fallback to environment variables passed by Docker (destroy compatibility)
        export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID}"
        export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY}"
        export AWS_REGION="${AWS_REGION:-us-east-2}"
        export AWS_DEFAULT_REGION="${AWS_REGION:-us-east-2}"
    fi

    cd "${QA_INFRA_WORK_PATH}" || {
        log_error "Failed to change to QA_INFRA_WORK_PATH: ${QA_INFRA_WORK_PATH}"
        exit 1
    }

    log_info 'Initializing OpenTofu with S3 backend configuration...'

    # Check if backend.tf exists and use appropriate initialization method
    if [ -f "tofu/aws/modules/airgap/backend.tf" ]; then
        log_info "Using backend.tf configuration"
        # Try init with -reconfigure and inspect output for backend-migration hints.
        set +e
        init_output=$(tofu -chdir=tofu/aws/modules/airgap init -input=false -upgrade -reconfigure 2>&1)
        init_rc=$?
        set -e
        if [ $init_rc -ne 0 ]; then
            log_warning "tofu init -reconfigure failed (rc=${init_rc})"
            log_debug "tofu init output: ${init_output}"
            # If output indicates a backend migration is required, try init with -migrate-state
            if echo "${init_output}" | grep -qiE "backend configuration|backend changed|migrate"; then
                log_info "Detected backend change/migration requirement; attempting init with -reconfigure -migrate-state"
                if tofu -chdir=tofu/aws/modules/airgap init -input=false -upgrade -reconfigure -migrate-state 2>&1; then
                    log_success "tofu init -reconfigure -migrate-state succeeded"
                else
                    log_warning "tofu init -reconfigure -migrate-state failed; falling back to standard init"
                    if ! tofu -chdir=tofu/aws/modules/airgap init -input=false -upgrade; then
                        log_error "tofu init failed for backend.tf after retries"
                        exit 1
                    fi
                fi
            else
                log_info "Backend change not detected; retrying standard init"
                if ! tofu -chdir=tofu/aws/modules/airgap init -input=false -upgrade; then
                    log_error "tofu init failed for backend.tf"
                    exit 1
                fi
            fi
        fi
    elif [ -f "tofu/aws/modules/airgap/${TERRAFORM_BACKEND_VARS_FILENAME}" ]; then
        log_info "Using backend.tfvars configuration"
        set +e
        init_output=$(tofu -chdir=tofu/aws/modules/airgap init -backend-config="${TERRAFORM_BACKEND_VARS_FILENAME}" -input=false -upgrade -reconfigure 2>&1)
        init_rc=$?
        set -e
        if [ $init_rc -ne 0 ]; then
            log_warning "tofu init with backend-config -reconfigure failed (rc=${init_rc})"
            log_debug "tofu init output: ${init_output}"
            if echo "${init_output}" | grep -qiE "backend configuration|backend changed|migrate"; then
                log_info "Detected backend change/migration requirement; attempting init with -reconfigure -migrate-state and backend-config"
                if tofu -chdir=tofu/aws/modules/airgap init -backend-config="${TERRAFORM_BACKEND_VARS_FILENAME}" -input=false -upgrade -reconfigure -migrate-state 2>&1; then
                    log_success "tofu init with backend-config -reconfigure -migrate-state succeeded"
                else
                    log_warning "tofu init with backend-config -reconfigure -migrate-state failed; retrying without -reconfigure"
                    if ! tofu -chdir=tofu/aws/modules/airgap init -backend-config="${TERRAFORM_BACKEND_VARS_FILENAME}" -input=false -upgrade; then
                        log_error "tofu init failed for backend.tfvars after retries"
                        exit 1
                    fi
                fi
            else
                log_info "Backend change not detected; retrying standard init with backend-config"
                if ! tofu -chdir=tofu/aws/modules/airgap init -backend-config="${TERRAFORM_BACKEND_VARS_FILENAME}" -input=false -upgrade; then
                    log_error "tofu init failed for backend.tfvars"
                    exit 1
                fi
            fi
        fi
    else
        log_error "Neither backend.tf nor backend.tfvars found"
        exit 1
    fi

    log_info 'Verifying initialization success...'
    tofu -chdir=tofu/aws/modules/airgap providers

    log_info "OpenTofu initialization completed successfully"
}

# Error handling
trap 'log_error "Script failed at line $LINENO"' ERR

# Execute main function
main "$@"
