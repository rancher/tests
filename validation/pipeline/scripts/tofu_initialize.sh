#!/bin/bash
set -e

# Consolidated OpenTofu initialization script for both airgap and destroy operations
# Supports both environment file sourcing and direct environment variable passing

# =============================================================================
# CONSTANTS
# =============================================================================

readonly SCRIPT_NAME="$(basename "$0")"
readonly SCRIPT_DIR="$(dirname "$0")"
readonly ENV_FILE="/tmp/.env"

# =============================================================================
# LOGGING FUNCTIONS
# =============================================================================

log_info() { echo "[INFO] $(date '+%Y-%m-%d %H:%M:%S') $*"; }
log_error() { echo "[ERROR] $(date '+%Y-%m-%d %H:%M:%S') $*" >&2; }

# =============================================================================
# PREREQUISITE VALIDATION
# =============================================================================

validate_prerequisites() {
  [[ -n "${QA_INFRA_WORK_PATH:-}" ]] || { log_error "QA_INFRA_WORK_PATH not set"; exit 1; }
  [[ -d "${QA_INFRA_WORK_PATH}" ]] || { log_error "QA_INFRA_WORK_PATH directory not found"; exit 1; }
  command -v tofu >/dev/null || { log_error "tofu not found"; exit 1; }
}

# =============================================================================
# MAIN FUNCTION
# =============================================================================

main() {
  log_info "Starting OpenTofu initialization with $SCRIPT_NAME"

  # Validate prerequisites
  validate_prerequisites

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
    export S3_REGION="${S3_REGION}"
    export S3_KEY_PREFIX="${S3_KEY_PREFIX}"
  else
    log_info "Environment file not found at $ENV_FILE, using Docker environment variables"
    # Fallback to environment variables passed by Docker (destroy compatibility)
    export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID}"
    export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY}"
    export AWS_REGION="${AWS_REGION:-us-east-2}"
    export AWS_DEFAULT_REGION="${AWS_REGION:-us-east-2}"
  fi

  cd "${QA_INFRA_WORK_PATH}"

echo 'DEBUG: Current working directory:'
pwd

echo 'DEBUG: Contents of tofu/aws/modules/airgap directory:'
ls -la tofu/aws/modules/airgap/

echo 'DEBUG: Checking if backend.tfvars file exists:'
if [ -f "tofu/aws/modules/airgap/${TERRAFORM_BACKEND_VARS_FILENAME}" ]; then
    echo "DEBUG: backend.tfvars file exists, contents:"
    cat "tofu/aws/modules/airgap/${TERRAFORM_BACKEND_VARS_FILENAME}"
else
    echo "DEBUG: backend.tfvars file does NOT exist"
fi

echo 'DEBUG: Checking if backend.tf file exists:'
if [ -f "tofu/aws/modules/airgap/backend.tf" ]; then
    echo "DEBUG: backend.tf file exists, contents:"
    cat "tofu/aws/modules/airgap/backend.tf"
else
    echo "DEBUG: backend.tf file does NOT exist"
fi

echo 'DEBUG: All .tf and .tfvars files in directory:'
find tofu/aws/modules/airgap/ -name "*.tf" -o -name "*.tfvars" | while read file; do
    echo "=== $file ==="
    cat "$file"
    echo
done

echo '=== END DEBUG ==='

echo 'Initializing OpenTofu with S3 backend configuration...'

# Check if backend.tf exists and use appropriate initialization method
if [ -f "tofu/aws/modules/airgap/backend.tf" ]; then
    echo "Using backend.tf configuration"
    tofu -chdir=tofu/aws/modules/airgap init -input=false -upgrade
elif [ -f "tofu/aws/modules/airgap/${TERRAFORM_BACKEND_VARS_FILENAME}" ]; then
    echo "Using backend.tfvars configuration"
    tofu -chdir=tofu/aws/modules/airgap init -backend-config="${TERRAFORM_BACKEND_VARS_FILENAME}" -input=false -upgrade
else
    echo "ERROR: Neither backend.tf nor backend.tfvars found"
    exit 1
fi

echo 'Verifying initialization success...'
tofu -chdir=tofu/aws/modules/airgap providers

  log_info "OpenTofu initialization completed successfully"
}

# Execute main function
main "$@"