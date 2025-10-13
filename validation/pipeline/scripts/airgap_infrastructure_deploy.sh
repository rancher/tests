#!/bin/bash
set -e

# Airgap Infrastructure Deployment Script
# Consolidated script that handles planning, applying, validating, and backing up infrastructure
# Replaces: airgap_plan_infrastructure.sh, airgap_apply_infrastructure.sh, airgap_validate_infrastructure.sh, airgap_backup_state.sh

# Load the airgap library
# Use absolute path since script may be executed from /tmp/
source "/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap_lib.sh"

# =============================================================================
# SCRIPT CONFIGURATION
# =============================================================================

readonly SCRIPT_NAME="$(basename "$0")"
readonly SCRIPT_DIR="$(dirname "$0")"

# =============================================================================
# MAIN DEPLOYMENT FUNCTION
# =============================================================================

deploy_infrastructure() {
    local workspace_name="${1:-$TF_WORKSPACE}"
    local var_file="${2:-$TERRAFORM_VARS_FILENAME}"
    local use_remote_path="${3:-true}"

    log_info "Starting infrastructure deployment for workspace: $workspace_name"

    # Determine which module path to use
    local module_path
    if [[ "$use_remote_path" == "true" ]]; then
        module_path="$REMOTE_TOFU_MODULE_PATH"
        log_info "Using remote module path: $module_path"
    else
        module_path="$TOFU_MODULE_PATH"
        log_info "Using local module path: $module_path"
    fi

    # Validate required variables
    validate_required_vars "QA_INFRA_WORK_PATH" "TF_WORKSPACE" "TERRAFORM_VARS_FILENAME"

    # Manage workspace first (before backend initialization)
    manage_workspace "$workspace_name" "$module_path"

    # Initialize OpenTofu (now that workspace exists)
    initialize_tofu "$module_path"

    # Generate and apply plan
    log_info "=== Infrastructure Planning Phase ==="
    generate_plan "$module_path" "$var_file" "tfplan"

    log_info "=== Infrastructure Deployment Phase ==="
    apply_plan "$module_path" "tfplan"

    # Backup state and generate outputs
    log_info "=== State Backup and Validation Phase ==="
    backup_state "$module_path"
    generate_outputs "$module_path"

    # Validate the deployment
    validate_infrastructure "$module_path"

    # Handle inventory file generation
    handle_inventory_file "$module_path"

    # Upload configuration to S3 if enabled
    if [[ "${UPLOAD_CONFIG_TO_S3:-true}" == "true" ]]; then
        upload_config_to_s3
    fi

    log_success "Infrastructure deployment completed successfully"
}

# =============================================================================
# INVENTORY HANDLING
# =============================================================================

handle_inventory_file() {
    local module_path="${1:-$REMOTE_TOFU_MODULE_PATH}"
    local shared_inventory="$SHARED_VOLUME_PATH/ansible-inventory.yml"
    local ansible_inventory="/root/ansible/rke2/airgap/inventory.yml"

    log_info "Handling inventory file generation"

    # Try multiple possible inventory file locations and names
    local inventory_files=(
        "$module_path/inventory.yml"
        "$module_path/inventory.yaml"
        "$module_path/ansible_inventory.yml"
        "$module_path/ansible_inventory.yaml"
        "$module_path/local_file.ansible_inventory.yml"
        "$module_path/local_file.ansible_inventory.yaml"
    )

    # Also look for any .yml or .yaml file that might be inventory-like
    while IFS= read -r -d '' file; do
        inventory_files+=("$file")
    done < <(find "$module_path" -maxdepth 1 -name "*.yml" -o -name "*.yaml" -print0 2>/dev/null)

    local found_inventory=""
    local inventory_file=""

    # Search for inventory file
    for candidate_file in "${inventory_files[@]}"; do
        if [[ -f "$candidate_file" && -s "$candidate_file" ]]; then
            # Check if it looks like an Ansible inventory file
            if grep -q "all:\|hosts:\|children:" "$candidate_file" 2>/dev/null; then
                found_inventory="$candidate_file"
                inventory_file="$candidate_file"
                log_success "Found Ansible inventory file: $inventory_file"
                break
            fi
        fi
    done

    # If no inventory found, search more broadly
    if [[ -z "$found_inventory" ]]; then
        log_info "Standard inventory search failed, searching for any YAML files with inventory content..."

        while IFS= read -r -d '' file; do
            if [[ -f "$file" && -s "$file" ]]; then
                # Check for inventory-like content
                if grep -q -E "(ansible_host|ansible_user|ansible_ssh_private_key_file)" "$file" 2>/dev/null; then
                    found_inventory="$file"
                    inventory_file="$file"
                    log_success "Found inventory-like file: $inventory_file"
                    break
                fi
            fi
        done < <(find "$module_path" -name "*.yml" -o -name "*.yaml" -print0 2>/dev/null)
    fi

    # If still no inventory found, check Terraform output and generate inventory
    if [[ -z "$found_inventory" ]]; then
        log_info "No inventory file found, checking Terraform outputs..."

        # Try to get inventory from Terraform output
        if tofu output -json > "$SHARED_VOLUME_PATH/tf-outputs-check.json" 2>/dev/null; then
            # Look for inventory content in outputs
            if grep -q "hosts\|inventory\|ansible" "$SHARED_VOLUME_PATH/tf-outputs-check.json" 2>/dev/null; then
                log_info "Found inventory-related Terraform outputs"
                # Copy outputs for debugging
                cp "$SHARED_VOLUME_PATH/tf-outputs-check.json" "$SHARED_VOLUME_PATH/inventory-debug-outputs.json"
            fi

            # Generate inventory file from Terraform outputs
            if generate_inventory_from_outputs "$SHARED_VOLUME_PATH/tf-outputs-check.json" "$module_path/inventory.yml"; then
                found_inventory="$module_path/inventory.yml"
                inventory_file="$module_path/inventory.yml"
                log_success "Generated inventory file from Terraform outputs: $inventory_file"
            fi
        fi
    fi

    # Process found inventory
    if [[ -n "$found_inventory" && -f "$inventory_file" ]]; then
        log_success "Processing inventory file: $inventory_file"

        # Copy to shared volume for artifact extraction
        cp "$inventory_file" "$shared_inventory"
        log_info "Inventory copied to shared volume: $shared_inventory"

        # Copy to Ansible expected location
        mkdir -p "$(dirname "$ansible_inventory")"
        cp "$inventory_file" "$ansible_inventory"
        log_info "Inventory copied to Ansible location: $ansible_inventory"

        # Show inventory content (truncated if too large)
        log_info "=== Inventory File Content ==="
        local inventory_lines
        inventory_lines=$(wc -l < "$inventory_file")
        log_info "Inventory file has $inventory_lines lines"

        if [[ $inventory_lines -le 50 ]]; then
            cat "$inventory_file"
        else
            head -20 "$inventory_file"
            log_info "... ($((inventory_lines - 40)) lines omitted) ..."
            tail -20 "$inventory_file"
        fi
        log_info "=== End Inventory Content ==="

        # Validate inventory syntax
        if validate_yaml_syntax "$inventory_file"; then
            log_success "Inventory file syntax is valid"
        else
            log_warning "Inventory file has syntax issues, but proceeding anyway"
        fi

    else
        log_error "No valid inventory file found in $module_path"
        log_warning "Searched for: $(printf ", %s" "${inventory_files[@]}")"
        log_warning "This may indicate a problem with the Terraform deployment"

        # List all files in module directory for debugging
        log_info "Files in module directory:"
        ls -la "$module_path" | head -20 || true

        # Create a debug file showing what we searched for
        {
            echo "Inventory File Search Results - $(date)"
            echo "Module path: $module_path"
            echo "Search patterns tried:"
            printf "  %s\n" "${inventory_files[@]}"
            echo ""
            echo "Files found in directory:"
            ls -la "$module_path" 2>/dev/null || echo "Directory not accessible"
        } > "$SHARED_VOLUME_PATH/inventory-search-debug.txt"

        return 1
    fi
}

# =============================================================================
# S3 UPLOAD FUNCTION
# =============================================================================

upload_config_to_s3() {
    local config_file="${1:-$TERRAFORM_VARS_FILENAME}"
    local s3_key="${S3_KEY_PREFIX}/cluster.tfvars"

    log_info "Uploading configuration file to S3: s3://$S3_BUCKET_NAME/$s3_key"

    # Validate AWS credentials
    validate_required_vars "AWS_ACCESS_KEY_ID" "AWS_SECRET_ACCESS_KEY" "S3_BUCKET_NAME"

    if [[ -f "$config_file" ]]; then
        if aws s3 cp "$config_file" "s3://$S3_BUCKET_NAME/$s3_key" --region "$S3_REGION"; then
            log_success "Configuration uploaded to S3 successfully"
        else
            log_error "Failed to upload configuration to S3"
            return 1
        fi
    else
        log_error "Configuration file not found: $config_file"
        return 1
    fi
}

# =============================================================================
# HELP AND USAGE
# =============================================================================

show_help() {
    cat << EOF
Usage: $SCRIPT_NAME [OPTIONS]

Airgap Infrastructure Deployment Script
This script consolidates infrastructure planning, deployment, validation, and backup operations.

OPTIONS:
    -w, --workspace WORKSPACE    Terraform workspace name (default: \$TF_WORKSPACE)
    -v, --var-file FILE         Terraform variables file (default: \$TERRAFORM_VARS_FILENAME)
    -l, --local-path           Use local module path instead of remote
    -h, --help                 Show this help message
    --no-s3-upload            Skip S3 upload of configuration file
    --debug                   Enable debug logging

ENVIRONMENT VARIABLES:
    TF_WORKSPACE                  Terraform workspace name
    TERRAFORM_VARS_FILENAME       Terraform variables file name
    TERRAFORM_BACKEND_VARS_FILENAME Terraform backend variables file name
    QA_INFRA_WORK_PATH           Path to qa-infra-automation repository
    S3_BUCKET_NAME               S3 bucket for state storage
    S3_REGION                    S3 region
    S3_KEY_PREFIX                S3 key prefix
    AWS_ACCESS_KEY_ID            AWS access key
    AWS_SECRET_ACCESS_KEY        AWS secret key
    AWS_REGION                   AWS region
    DEBUG                        Enable debug logging (true/false)
    UPLOAD_CONFIG_TO_S3          Upload config to S3 (true/false, default: true)

EXAMPLES:
    # Deploy with default settings
    $SCRIPT_NAME

    # Deploy with specific workspace and variables
    $SCRIPT_NAME -w my-workspace -v my-vars.tfvars

    # Deploy using local path and debug logging
    DEBUG=true $SCRIPT_NAME -l --debug

    # Deploy without S3 upload
    $SCRIPT_NAME --no-s3-upload

EOF
}

# =============================================================================
# ARGUMENT PARSING
# =============================================================================

parse_arguments() {
    local workspace="$TF_WORKSPACE"
    local var_file="$TERRAFORM_VARS_FILENAME"
    local use_remote_path="true"
    local upload_to_s3="true"

    while [[ $# -gt 0 ]]; do
        case $1 in
            -w|--workspace)
                workspace="$2"
                shift 2
                ;;
            -v|--var-file)
                var_file="$2"
                shift 2
                ;;
            -l|--local-path)
                use_remote_path="false"
                shift
                ;;
            --no-s3-upload)
                upload_to_s3="false"
                shift
                ;;
            --debug)
                export DEBUG="true"
                shift
                ;;
            -h|--help)
                show_help
                exit 0
                ;;
            *)
                log_error "Unknown option: $1"
                show_help
                exit 1
                ;;
        esac
    done

    # Export variables for use in functions
    export TF_WORKSPACE="$workspace"
    export TERRAFORM_VARS_FILENAME="$var_file"
    export UPLOAD_CONFIG_TO_S3="$upload_to_s3"

    log_info "Configuration:"
    log_info "  Workspace: $workspace"
    log_info "  Variables file: $var_file"
    log_info "  Use remote path: $use_remote_path"
    log_info "  Upload to S3: $upload_to_s3"
    log_info "  Debug mode: ${DEBUG:-false}"
}

# =============================================================================
# MAIN SCRIPT EXECUTION
# =============================================================================

main() {
    log_info "=== Airgap Infrastructure Deployment Started ==="
    log_info "Script: $SCRIPT_NAME"
    log_info "Timestamp: $(date)"
    log_info "Working directory: $(pwd)"

    # Parse command line arguments
    parse_arguments "$@"

    # Initialize the airgap environment
    initialize_airgap_environment

    # Wait for confirmation if in interactive mode
    wait_for_confirmation "Press Enter to start infrastructure deployment..."

    # Run the deployment
    deploy_infrastructure "$TF_WORKSPACE" "$TERRAFORM_VARS_FILENAME" "$use_remote_path"

    log_success "=== Airgap Infrastructure Deployment Completed ==="
}

# Error handling
trap 'log_error "Script failed at line $LINENO"' ERR

# Execute main function with all arguments
main "$@"