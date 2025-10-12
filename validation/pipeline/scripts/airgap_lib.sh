#!/bin/bash
# Airgap Deployment Library
# Common functions and utilities for airgap Rancher/RKE2 deployment
# This library consolidates duplicated functionality across multiple scripts

set -e

# =============================================================================
# GLOBAL VARIABLES AND CONFIGURATION
# =============================================================================

# Default values
export AWS_REGION="${AWS_REGION:-us-east-2}"
export AWS_DEFAULT_REGION="${AWS_DEFAULT_REGION:-us-east-2}"
export S3_BUCKET_NAME="${S3_BUCKET_NAME:-jenkins-terraform-state-storage}"
export S3_REGION="${S3_REGION:-us-east-2}"
export S3_KEY_PREFIX="${S3_KEY_PREFIX:-jenkins-airgap-rke2}"

# Common paths
export QA_INFRA_PATH="${QA_INFRA_WORK_PATH}"
export TOFU_MODULE_PATH="${QA_INFRA_PATH}/tofu/aws/modules/airgap"
export REMOTE_TOFU_MODULE_PATH="/root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap"
export SHARED_VOLUME_PATH="/root"

# =============================================================================
# LOGGING AND DEBUG FUNCTIONS
# =============================================================================

# Colors for output
readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly BLUE='\033[0;34m'
readonly NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $1" >&2
}

# Debug logging (only shows if DEBUG=true)
log_debug() {
    if [[ "${DEBUG:-false}" == "true" ]]; then
        echo -e "${BLUE}[DEBUG]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $1"
    fi
}

# =============================================================================
# ENVIRONMENT AND CONFIGURATION FUNCTIONS
# =============================================================================

# Source environment file if it exists and load variables
load_environment() {
    local env_file="${1:-/tmp/.env}"

    if [[ -f "$env_file" ]]; then
        log_info "Sourcing environment file: $env_file"
        source "$env_file"

        # Export all variables to ensure they're available to child processes
        export S3_BUCKET_NAME S3_REGION S3_KEY_PREFIX TF_WORKSPACE
        export TERRAFORM_VARS_FILENAME TERRAFORM_BACKEND_VARS_FILENAME

        log_debug "Environment file loaded successfully"
    else
        log_debug "Environment file not found at $env_file, using Docker environment variables"
    fi
}

# Export AWS credentials for OpenTofu
export_aws_credentials() {
    log_debug "Setting AWS credentials and region"

    export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID}"
    export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY}"
    export AWS_REGION="${AWS_REGION:-us-east-2}"
    export AWS_DEFAULT_REGION="${AWS_DEFAULT_REGION:-us-east-2}"

    if [[ -z "${AWS_ACCESS_KEY_ID}" || -z "${AWS_SECRET_ACCESS_KEY}" ]]; then
        log_error "AWS credentials are not properly set"
        return 1
    fi

    log_debug "AWS credentials configured"
}

# Validate required environment variables
validate_required_vars() {
    local required_vars=("$@")
    local missing_vars=()

    for var in "${required_vars[@]}"; do
        if [[ -z "${!var}" ]]; then
            missing_vars+=("$var")
        fi
    done

    if [[ ${#missing_vars[@]} -gt 0 ]]; then
        log_error "Missing required environment variables: ${missing_vars[*]}"
        return 1
    fi

    log_debug "All required variables are set: ${required_vars[*]}"
}

# =============================================================================
# TERRAFORM/OPENTOFU FUNCTIONS
# =============================================================================

# Initialize OpenTofu with proper backend configuration
initialize_tofu() {
    local module_path="${1:-$TOFU_MODULE_PATH}"

    log_info "Initializing OpenTofu in: $module_path"

    cd "$module_path" || {
        log_error "Failed to change to directory: $module_path"
        return 1
    }

    # Check for backend configuration
    if [[ -f "backend.tf" ]]; then
        log_info "Using backend.tf configuration"
        tofu init -input=false -upgrade
    elif [[ -n "${TERRAFORM_BACKEND_VARS_FILENAME}" && -f "${TERRAFORM_BACKEND_VARS_FILENAME}" ]]; then
        log_info "Using backend.tfvars configuration: ${TERRAFORM_BACKEND_VARS_FILENAME}"
        tofu init -backend-config="${TERRAFORM_BACKEND_VARS_FILENAME}" -input=false -upgrade
    else
        log_error "Neither backend.tf nor backend.tfvars found"
        return 1
    fi

    log_success "OpenTofu initialization completed"
}

# Manage Terraform workspace
manage_workspace() {
    local workspace_name="${1:-$TF_WORKSPACE}"
    local module_path="${2:-$TOFU_MODULE_PATH}"

    if [[ -z "$workspace_name" ]]; then
        log_error "Workspace name is required"
        return 1
    fi

    log_info "Managing workspace: $workspace_name"

    cd "$module_path" || {
        log_error "Failed to change to directory: $module_path"
        return 1
    }

    # First, do a basic initialization without backend if needed
    if [[ ! -f ".terraform" ]] || [[ ! -d ".terraform" ]]; then
        log_info "Terraform not initialized, doing basic init first"
        tofu init -input=false -upgrade || {
            log_error "Failed to initialize Terraform for workspace management"
            return 1
        }
    fi

    # Temporarily unset TF_WORKSPACE to avoid automatic selection
    local current_tf_workspace="$TF_WORKSPACE"
    unset TF_WORKSPACE

    # Check if workspace exists
    local workspace_exists
    workspace_exists=$(tofu workspace list 2>/dev/null | grep -w "$workspace_name" || true)

    if [[ -z "$workspace_exists" ]]; then
        log_info "Creating workspace: $workspace_name"
        tofu workspace new "$workspace_name"
        log_success "Workspace created: $workspace_name"
    else
        log_info "Workspace already exists: $workspace_name"
    fi

    # Select the workspace
    tofu workspace select "$workspace_name"

    # Restore TF_WORKSPACE
    export TF_WORKSPACE="$current_tf_workspace"

    # Verify workspace selection
    local current_workspace
    current_workspace=$(tofu workspace show)

    if [[ "$current_workspace" != "$workspace_name" ]]; then
        log_error "Failed to select workspace $workspace_name. Current: $current_workspace"
        return 1
    fi

    log_success "Workspace selected: $workspace_name"
}

# Generate Terraform plan
generate_plan() {
    local module_path="${1:-$TOFU_MODULE_PATH}"
    local var_file="${2:-$TERRAFORM_VARS_FILENAME}"
    local plan_output="${3:-tfplan}"

    log_info "Generating Terraform plan in: $module_path"

    cd "$module_path" || {
        log_error "Failed to change to directory: $module_path"
        return 1
    }

    local var_file_arg=""
    if [[ -n "$var_file" && -f "$var_file" ]]; then
        var_file_arg="-var-file=$var_file"
        log_info "Using var file: $var_file"
    else
        log_warning "No var file specified or found"
    fi

    # Generate plan
    tofu plan $var_file_arg -input=false -out="$plan_output"

    # Verify plan file exists and is not empty
    if [[ ! -f "$plan_output" ]]; then
        log_error "Plan file was not generated: $plan_output"
        return 1
    fi

    local plan_size
    plan_size=$(stat -c%s "$plan_output" 2>/dev/null || echo 0)
    if [[ "$plan_size" -eq 0 ]]; then
        log_error "Plan file is empty: $plan_output"
        return 1
    fi

    log_success "Plan generated successfully ($plan_size bytes): $plan_output"

    # Copy plan to shared volume if path is different
    if [[ "$module_path" != "$SHARED_VOLUME_PATH" ]]; then
        cp "$plan_output" "$SHARED_VOLUME_PATH/${plan_output}-backup"
        log_info "Plan backed up to shared volume"
    fi
}

# Apply Terraform plan
apply_plan() {
    local module_path="${1:-$TOFU_MODULE_PATH}"
    local plan_file="${2:-tfplan}"

    log_info "Applying Terraform plan in: $module_path"

    cd "$module_path" || {
        log_error "Failed to change to directory: $module_path"
        return 1
    }

    # Check if plan file exists
    if [[ ! -f "$plan_file" ]]; then
        log_warning "Plan file not found: $plan_file, generating new plan..."
        generate_plan "$module_path" "$TERRAFORM_VARS_FILENAME" "$plan_file"
    fi

    # Apply the plan
    if ! tofu apply -auto-approve -input=false "$plan_file"; then
        log_warning "Apply failed, attempting without plan file (stale plan recovery)..."
        if tofu apply -auto-approve -input=false; then
            log_success "Recovery apply completed successfully"
        else
            log_error "Terraform apply failed even without plan file"
            return 1
        fi
    else
        log_success "Terraform apply completed successfully"
    fi
}

# Destroy infrastructure
destroy_infrastructure() {
    local module_path="${1:-$TOFU_MODULE_PATH}"
    local var_file="${2:-$TERRAFORM_VARS_FILENAME}"

    log_info "Destroying infrastructure in: $module_path"

    cd "$module_path" || {
        log_error "Failed to change to directory: $module_path"
        return 1
    }

    local var_file_arg=""
    if [[ -n "$var_file" && -f "$var_file" ]]; then
        var_file_arg="-var-file=$var_file"
        log_info "Using var file: $var_file"
    fi

    # Perform destruction
    if tofu destroy $var_file_arg -auto-approve -input=false; then
        log_success "Infrastructure destroyed successfully"

        # Clean up workspace if requested
        if [[ "${CLEANUP_WORKSPACE:-true}" == "true" ]]; then
            cleanup_workspace "$TF_WORKSPACE" "$module_path"
        fi

        return 0
    else
        log_error "Infrastructure destruction failed"
        return 1
    fi
}

# Cleanup Terraform workspace
cleanup_workspace() {
    local workspace_name="${1:-$TF_WORKSPACE}"
    local module_path="${2:-$TOFU_MODULE_PATH}"

    log_info "Cleaning up workspace: $workspace_name"

    cd "$module_path" || {
        log_error "Failed to change to directory: $module_path"
        return 1
    }

    # Switch to default workspace
    unset TF_WORKSPACE
    tofu workspace select default || log_warning "Could not switch to default workspace"

    # Delete the workspace
    tofu workspace delete "$workspace_name" || log_warning "Could not delete workspace: $workspace_name"

    log_success "Workspace cleanup completed"
}

# =============================================================================
# STATE AND OUTPUT FUNCTIONS
# =============================================================================

# Backup Terraform state
backup_state() {
    local module_path="${1:-$TOFU_MODULE_PATH}"
    local backup_suffix="${2:-$(date +%Y%m%d-%H%M%S)}"

    log_info "Backing up Terraform state from: $module_path"

    cd "$module_path" || {
        log_error "Failed to change to directory: $module_path"
        return 1
    }

    local state_file="terraform.tfstate"
    local backup_file="$state_file.backup-$backup_suffix"

    # Handle local state file
    if [[ -f "$state_file" ]]; then
        cp "$state_file" "$backup_file"
        cp "$state_file" "$SHARED_VOLUME_PATH/terraform-state-primary.tfstate"
        cp "$state_file" "$SHARED_VOLUME_PATH/terraform.tfstate"

        local state_size
        state_size=$(stat -c%s "$state_file" 2>/dev/null || echo 0)
        log_success "Local state backed up ($state_size bytes)"
    else
        log_info "Local state file not found, attempting to pull from remote backend"

        # Pull from remote backend
        if tofu state pull > "$SHARED_VOLUME_PATH/terraform.tfstate" 2>/dev/null; then
            if [[ -s "$SHARED_VOLUME_PATH/terraform.tfstate" ]]; then
                cp "$SHARED_VOLUME_PATH/terraform.tfstate" "$backup_file"
                cp "$SHARED_VOLUME_PATH/terraform.tfstate" "$SHARED_VOLUME_PATH/terraform-state-primary.tfstate"

                local state_size
                state_size=$(stat -c%s "$SHARED_VOLUME_PATH/terraform.tfstate" 2>/dev/null || echo 0)
                log_success "Remote state pulled and backed up ($state_size bytes)"
            else
                log_error "Pulled state file is empty"
                return 1
            fi
        else
            log_error "Failed to pull state from remote backend"
            return 1
        fi
    fi

    # Backup variables file
    if [[ -n "${TERRAFORM_VARS_FILENAME}" && -f "${TERRAFORM_VARS_FILENAME}" ]]; then
        cp "${TERRAFORM_VARS_FILENAME}" "$SHARED_VOLUME_PATH/${TERRAFORM_VARS_FILENAME}"
        log_info "Variables file backed up"
    fi

    log_success "State backup completed"
}

# Generate and validate outputs
generate_outputs() {
    local module_path="${1:-$TOFU_MODULE_PATH}"
    local output_file="${2:-$SHARED_VOLUME_PATH/infrastructure-outputs.json}"

    log_info "Generating outputs from: $module_path"

    cd "$module_path" || {
        log_error "Failed to change to directory: $module_path"
        return 1
    }

    # Generate outputs
    if tofu output -json > "$output_file" 2>&1; then
        local output_size
        output_size=$(stat -c%s "$output_file" 2>/dev/null || echo 0)

        if [[ "$output_size" -gt 0 ]]; then
            log_success "Outputs generated ($output_size bytes): $output_file"
        else
            log_warning "Outputs file is empty: $output_file"
        fi
    else
        log_error "Failed to generate outputs"
        return 1
    fi
}

# Validate infrastructure state
validate_infrastructure() {
    local module_path="${1:-$TOFU_MODULE_PATH}"

    log_info "Validating infrastructure state in: $module_path"

    cd "$module_path" || {
        log_error "Failed to change to directory: $module_path"
        return 1
    }

    # Get state list
    local state_list_file="$SHARED_VOLUME_PATH/state-list.txt"
    if tofu state list > "$state_list_file" 2>&1; then
        local state_count
        state_count=$(wc -l < "$state_list_file")

        if [[ "$state_count" -eq 0 || "$state_count" -eq 1 ]]; then
            log_warning "No resources found in state"
        else
            log_success "State contains $((state_count - 1)) resources"
        fi
    else
        log_error "Failed to retrieve state list"
        return 1
    fi

    # Generate outputs to validate connectivity
    generate_outputs "$module_path"

    log_success "Infrastructure validation completed"
}

# =============================================================================
# ANSIBLE-RELATED FUNCTIONS
# =============================================================================

# Generate Ansible group_vars from ANSIBLE_VARIABLES
generate_group_vars() {
    local output_dir="${1:-$SHARED_VOLUME_PATH/group_vars}"
    local ansible_vars_content="${ANSIBLE_VARIABLES}"

    log_info "Generating Ansible group_vars"

    # Validate input
    if [[ -z "$ansible_vars_content" && -n "${ANSIBLE_VARIABLES_FILE}" ]]; then
        if [[ -f "${ANSIBLE_VARIABLES_FILE}" ]]; then
            ansible_vars_content="$(cat "${ANSIBLE_VARIABLES_FILE}")"
        else
            log_error "ANSIBLE_VARIABLES_FILE not found: ${ANSIBLE_VARIABLES_FILE}"
            return 1
        fi
    fi

    if [[ -z "$ansible_vars_content" ]]; then
        log_error "ANSIBLE_VARIABLES environment variable is not set"
        return 1
    fi

    # Create output directory
    mkdir -p "$output_dir"
    local output_file="$output_dir/all.yml"

    # Create file header
    cat > "$output_file" <<'EOF'
---
# Ansible group variables for Rancher/RKE2 deployment
# This file is auto-generated by airgap library
# All variables come from ANSIBLE_VARIABLES parameter

EOF

    # Process ANSIBLE_VARIABLES to replace placeholders
    local processed_vars="$ansible_vars_content"

    # Use sed for variable replacement (only replace ${VAR} patterns)
    local var_names=("RKE2_VERSION" "RANCHER_VERSION" "HOSTNAME_PREFIX" "RANCHER_HOSTNAME"
                     "PRIVATE_REGISTRY_URL" "PRIVATE_REGISTRY_USERNAME" "PRIVATE_REGISTRY_PASSWORD")

    for var_name in "${var_names[@]}"; do
        if [[ -n "${!var_name}" ]]; then
            local replacement
            replacement="$(echo "${!var_name}" | sed 's/[[\.*^$()+?{|]/\\&/g')"
            processed_vars="$(echo "$processed_vars" | sed "s/\\\${$var_name}/$replacement/g")"
        fi
    done

    # Clean up any remaining unmatched variable patterns
    processed_vars="$(echo "$processed_vars" | sed 's/\$[^{}]*}/}/g')"

    # Write processed content
    printf '%s\n' "$processed_vars" | tr -d '\r' | sed 's/^---//' >> "$output_file"

    # Validate YAML syntax (if safe to do so)
    validate_yaml_syntax "$output_file"

    # Copy to standard Ansible location
    local target_dir="/root/ansible/rke2/airgap/group_vars"
    if mkdir -p "$target_dir" 2>/dev/null; then
        cp "$output_file" "$target_dir/all.yml"
        log_info "group_vars copied to $target_dir"
    fi

    log_success "group_vars generated: $output_file"
}

# Validate YAML syntax
validate_yaml_syntax() {
    local yaml_file="$1"

    # Skip validation if templating tokens are present
    if grep -q "{{" "$yaml_file" 2>/dev/null || grep -q "}}" "$yaml_file" 2>/dev/null; then
        log_info "Skipping YAML validation (templating tokens detected)"
        return 0
    fi

    # Try python3 validation first
    if command -v python3 &>/dev/null; then
        if python3 -c "import yaml; yaml.safe_load(open('$yaml_file'))" 2>/dev/null; then
            log_info "YAML syntax validation passed"
            return 0
        else
            log_error "YAML syntax validation failed"
            return 1
        fi
    fi

    # Try yamllint
    if command -v yamllint &>/dev/null; then
        if yamllint "$yaml_file" >/dev/null 2>&1; then
            log_info "YAML lint validation passed"
            return 0
        else
            log_error "YAML lint validation failed"
            return 1
        fi
    fi

    log_info "No YAML validation tool available, proceeding without validation"
    return 0
}

# Setup SSH keys for Ansible
setup_ssh_keys() {
    log_info "Setting up SSH keys for Ansible"

    # Create SSH directory
    mkdir -p /root/.ssh

    if [[ -n "$AWS_SSH_PEM_KEY" ]]; then
        local temp_key="/tmp/ssh_key.pem"

        # Handle base64 encoded keys
        if echo "$AWS_SSH_PEM_KEY" | grep -q "^LS0t"; then
            log_info "Decoding base64 SSH key"
            echo "$AWS_SSH_PEM_KEY" | base64 -d > "$temp_key"
        else
            echo "$AWS_SSH_PEM_KEY" > "$temp_key"
        fi

        chmod 600 "$temp_key"

        # Extract public key
        if ssh-keygen -y -f "$temp_key" > /root/.ssh/authorized_keys 2>/dev/null; then
            chmod 600 /root/.ssh/authorized_keys
            log_success "SSH keys configured successfully"
        else
            log_error "Failed to extract public key from SSH private key"
            touch /root/.ssh/authorized_keys
            chmod 600 /root/.ssh/authorized_keys
            return 1
        fi

        # Clean up
        rm -f "$temp_key"
    else
        log_warning "AWS_SSH_PEM_KEY not set, creating empty authorized_keys"
        touch /root/.ssh/authorized_keys
        chmod 600 /root/.ssh/authorized_keys
    fi
}

# =============================================================================
# UTILITY FUNCTIONS
# =============================================================================

# Create backup of file with timestamp
backup_file() {
    local file_path="$1"
    local backup_suffix="${2:-$(date +%Y%m%d-%H%M%S)}"

    if [[ -f "$file_path" ]]; then
        local backup_file="${file_path}.backup-${backup_suffix}"
        cp "$file_path" "$backup_file"
        log_info "File backed up: $file_path -> $backup_file"
        echo "$backup_file"
    else
        log_warning "File not found for backup: $file_path"
        return 1
    fi
}

# Create cleanup artifacts archive
create_cleanup_archive() {
    local archive_name="$1"
    shift
    local files=("$@")

    log_info "Creating cleanup archive: $archive_name"

    # Filter existing files
    local existing_files=()
    for file in "${files[@]}"; do
        if [[ -f "$file" ]]; then
            existing_files+=("$file")
        fi
    done

    if [[ ${#existing_files[@]} -gt 0 ]]; then
        tar -czf "$archive_name" "${existing_files[@]}" 2>/dev/null || {
            log_error "Failed to create cleanup archive"
            return 1
        }
        log_success "Cleanup archive created: $archive_name"
    else
        log_warning "No files found for cleanup archive"
    fi
}

# Wait for user confirmation (useful for debugging)
wait_for_confirmation() {
    local message="${1:-Press Enter to continue...}"

    if [[ "${INTERACTIVE:-false}" == "true" ]]; then
        echo -n "$message "
        read -r
        log_info "User confirmation received"
    fi
}

# =============================================================================
# INITIALIZATION
# =============================================================================

# This function should be called at the beginning of any script using this library
initialize_airgap_environment() {
    log_info "Initializing airgap environment"

    # Load environment variables
    load_environment

    # Export AWS credentials
    export_aws_credentials

    # Validate basic required variables
    local basic_vars=("QA_INFRA_WORK_PATH" "TF_WORKSPACE")
    validate_required_vars "${basic_vars[@]}"

    log_success "Airgap environment initialized"
}

# Export all functions for use in other scripts
export -f log_info log_success log_warning log_error log_debug
export -f load_environment export_aws_credentials validate_required_vars
export -f initialize_tofu manage_workspace generate_plan apply_plan destroy_infrastructure cleanup_workspace
export -f backup_state generate_outputs validate_infrastructure
export -f generate_group_vars validate_yaml_syntax setup_ssh_keys
export -f backup_file create_cleanup_archive wait_for_confirmation
export -f initialize_airgap_environment

log_info "Airgap library loaded successfully"