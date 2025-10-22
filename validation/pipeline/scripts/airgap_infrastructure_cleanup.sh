#!/bin/bash
set -e

# Airgap Infrastructure Cleanup Script
# Consolidated script that handles infrastructure destruction and workspace management
# Replaces: destroy_execute.sh, destroy_download_config.sh, destroy_validate_state.sh, tofu_delete_workspace.sh

# =============================================================================
# CONSTANTS
# =============================================================================

readonly SCRIPT_NAME="$(basename "$0")"
readonly SCRIPT_DIR="$(dirname "$0")"
readonly QA_INFRA_CLONE_PATH="/root/qa-infra-automation"

# =============================================================================
# LOGGING FUNCTIONS
# =============================================================================

log_info() { echo "[INFO] $(date '+%Y-%m-%d %H:%M:%S') $*"; }
log_error() { echo "[ERROR] $(date '+%Y-%m-%d %H:%M:%S') $*" >&2; }

# =============================================================================
# PREREQUISITE VALIDATION
# =============================================================================

validate_prerequisites() {
  # If logging helper already exists, assume airgap library is loaded
  if type log_info >/dev/null 2>&1; then
    return 0
  fi

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

  command -v tofu >/dev/null || { log_error "tofu not found"; exit 1; }
}

# =============================================================================
# SCRIPT CONFIGURATION
# =============================================================================

# Load the airgap library
# Use absolute path since script may be executed from /tmp/
source "/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap_lib.sh"

# =============================================================================
# CLEANUP OPERATIONS
# =============================================================================

cleanup_infrastructure() {
    local workspace_name="${1:-$TF_WORKSPACE}"
    local var_file="${2:-$TERRAFORM_VARS_FILENAME}"
    local use_remote_path="${3:-true}"
    local cleanup_workspace="${4:-true}"

    log_info "Starting infrastructure cleanup for workspace: $workspace_name"

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
    validate_required_vars "QA_INFRA_WORK_PATH" "TF_WORKSPACE"

    # Create cleanup log
    local cleanup_log="$SHARED_VOLUME_PATH/infrastructure-cleanup.log"
    cat > "$cleanup_log" << EOF
# Infrastructure Cleanup Log
# Generated on: $(date)
# Workspace: $workspace_name
# Module path: $module_path
# Variables file: $var_file

Cleanup operations performed:
EOF

    # Log function for cleanup operations
    log_cleanup() {
        local message="$1"
        echo "[$(date)] $message" >> "$cleanup_log"
        log_info "$message"
    }

    # Initialize cleanup
    log_cleanup "Starting infrastructure cleanup"
    log_cleanup "Working directory: $(pwd)"

    # Gather information before cleanup
    log_cleanup "Gathering pre-cleanup infrastructure state..."
    gather_cleanup_information "$module_path" "$cleanup_log"

    # Ensure backend files and var file are available before initializing OpenTofu
    log_cleanup "Ensuring backend configuration and terraform var file are present in module path"

    # Attempt to download cluster.tfvars from S3 into shared volume and module path
    if download_cluster_tfvars_from_s3 "$workspace_name" "$var_file" "$module_path"; then
        log_cleanup "cluster.tfvars is available in module path"
    else
        log_cleanup "WARNING: cluster.tfvars not fetched from S3 workspace; proceeding (var file may be absent)"
    fi

    # Generate backend.tf / backend.tfvars in module path to ensure tofu init has backend config
    if generate_backend_files "$module_path"; then
        log_cleanup "Backend configuration generated in module path"
    else
        log_cleanup "WARNING: Failed to generate backend configuration in module path"
    fi

    # Initialize OpenTofu
    if initialize_tofu "$module_path"; then
        log_cleanup "OpenTofu initialization successful"
    else
        log_cleanup "WARNING: OpenTofu initialization failed, proceeding with cleanup"
    fi

    # Select workspace if specified
    if [[ -n "$workspace_name" ]]; then
        log_cleanup "Switching to workspace: $workspace_name"
        export TF_WORKSPACE="$workspace_name"

        # Use improved workspace selection function
        if select_workspace "$workspace_name" "$module_path" "false"; then
            log_cleanup "Successfully switched to workspace: $workspace_name"
        else
            log_cleanup "WARNING: Could not switch to workspace $workspace_name (may not exist)"
            log_cleanup "Proceeding with cleanup in current workspace"
        fi
    fi

    # Perform infrastructure destruction
    log_cleanup "Starting infrastructure destruction..."
    if destroy_infrastructure "$module_path" "$var_file"; then
        log_cleanup "✓ Infrastructure destruction completed successfully"
    else
        log_cleanup "✗ Infrastructure destruction failed or had issues"

        # Try to get remaining resources
        if tofu state list > "$SHARED_VOLUME_PATH/remaining-resources.txt" 2>/dev/null; then
            local remaining_count
            remaining_count=$(wc -l < "$SHARED_VOLUME_PATH/remaining-resources.txt")
            log_cleanup "Remaining resources: $remaining_count"
            if [[ $remaining_count -gt 0 ]]; then
                log_cleanup "Remaining resources list:"
                cat "$SHARED_VOLUME_PATH/remaining-resources.txt" >> "$cleanup_log"
            fi
        fi
    fi

    # Clean up workspace if requested
    if [[ "$cleanup_workspace" == "true" && -n "$workspace_name" ]]; then
        log_cleanup "Cleaning up workspace: $workspace_name"
        cleanup_workspace "$workspace_name" "$module_path"
    fi

    # Generate cleanup report
    generate_cleanup_report "$workspace_name" "$cleanup_log"

    # Create cleanup artifacts archive
    create_cleanup_artifacts "$cleanup_log"

    log_success "Infrastructure cleanup completed"
}

# =============================================================================
# INFORMATION GATHERING
# =============================================================================

gather_cleanup_information() {
    local module_path="$1"
    local cleanup_log="$2"

    cd "$module_path" || {
        echo "[$(date)] Failed to change to module directory: $module_path" >> "$cleanup_log"
        return 1
    }

    # Get current workspace
    local current_workspace
    current_workspace=$(tofu workspace show 2>/dev/null || echo "unknown")
    echo "[$(date)] Current workspace: $current_workspace" >> "$cleanup_log"

    # Get resource list
    log_cleanup "Extracting resource list from Terraform state..."
    if tofu state list > "$SHARED_VOLUME_PATH/pre-cleanup-state-list.txt" 2>/dev/null; then
        local resource_count
        resource_count=$(wc -l < "$SHARED_VOLUME_PATH/pre-cleanup-state-list.txt")
        log_cleanup "Found $resource_count resources in state"
        echo "[$(date)] Pre-cleanup resource count: $resource_count" >> "$cleanup_log"
    else
        log_cleanup "Failed to extract resource list (state may be empty or inaccessible)"
    fi

    # Get outputs
    log_cleanup "Extracting Terraform outputs..."
    if tofu output -json > "$SHARED_VOLUME_PATH/pre-cleanup-outputs.json" 2>/dev/null; then
        log_cleanup "Terraform outputs extracted successfully"
    else
        log_cleanup "Failed to extract Terraform outputs (may not exist)"
    fi

    # List available workspaces
    log_cleanup "Listing available workspaces..."
    if tofu workspace list > "$SHARED_VOLUME_PATH/available-workspaces.txt" 2>/dev/null; then
        log_cleanup "Available workspaces listed"
    else
        log_cleanup "Failed to list workspaces"
    fi
}

# =============================================================================
# REPORT GENERATION
# =============================================================================

generate_cleanup_report() {
    local workspace_name="$1"
    local cleanup_log="$2"

    local report_file="$SHARED_VOLUME_PATH/infrastructure-cleanup-report.txt"

    cat > "$report_file" << EOF
# Infrastructure Cleanup Report
# Generated on: $(date)
# Workspace: $workspace_name

Cleanup Summary:
- Start time: $(date)
- Workspace: $workspace_name
- Script: $SCRIPT_NAME

Actions Taken:
1. Gathered pre-cleanup infrastructure information
2. Initialized OpenTofu backend
3. Selected target workspace
4. Attempted infrastructure destruction
5. Cleaned up workspace (if requested)

Pre-cleanup State:
EOF

    # Add resource count information
    if [[ -f "$SHARED_VOLUME_PATH/pre-cleanup-state-list.txt" ]]; then
        local resource_count
        resource_count=$(wc -l < "$SHARED_VOLUME_PATH/pre-cleanup-state-list.txt")
        echo "- Resources in state: $resource_count" >> "$report_file"
    fi

    # Add workspace information
    if [[ -f "$SHARED_VOLUME_PATH/available-workspaces.txt" ]]; then
        echo "- Available workspaces: $(cat "$SHARED_VOLUME_PATH/available-workspaces.txt" | tr '\n' ' ')" >> "$report_file"
    fi

    # Add outputs information
    if [[ -f "$SHARED_VOLUME_PATH/pre-cleanup-outputs.json" ]]; then
        echo "- Terraform outputs: Available" >> "$report_file"
    else
        echo "- Terraform outputs: Not available" >> "$report_file"
    fi

    cat >> "$report_file" << EOF

Post-cleanup Status:
EOF

    # Check for remaining resources
    if [[ -f "$SHARED_VOLUME_PATH/remaining-resources.txt" ]]; then
        local remaining_count
        remaining_count=$(wc -l < "$SHARED_VOLUME_PATH/remaining-resources.txt")
        if [[ $remaining_count -eq 0 ]]; then
            echo "- Remaining resources: None (cleanup successful)" >> "$report_file"
        else
            echo "- Remaining resources: $remaining_count (cleanup incomplete)" >> "$report_file"
        fi
    else
        echo "- Remaining resources: Unknown (state check failed)" >> "$report_file"
    fi

    cat >> "$report_file" << EOF

Artifacts Generated:
- $cleanup_log
- $SHARED_VOLUME_PATH/pre-cleanup-state-list.txt
- $SHARED_VOLUME_PATH/pre-cleanup-outputs.json
- $SHARED_VOLUME_PATH/available-workspaces.txt
- $SHARED_VOLUME_PATH/remaining-resources.txt
- $SHARED_VOLUME_PATH/infrastructure-cleanup-report.txt
- $SHARED_VOLUME_PATH/infrastructure-cleanup-artifacts.tar.gz

EOF

    # Add recommendations based on cleanup status
    if [[ -f "$SHARED_VOLUME_PATH/remaining-resources.txt" ]]; then
        local remaining_count
        remaining_count=$(wc -l < "$SHARED_VOLUME_PATH/remaining-resources.txt")
        if [[ $remaining_count -gt 0 ]]; then
            cat >> "$report_file" << EOF
Recommendations:
- Manual cleanup may be required for remaining resources
- Check the remaining-resources.txt file for details
- Consider running the cleanup script again
- Verify AWS console for any resources not managed by Terraform

EOF
        else
            cat >> "$report_file" << EOF
Recommendations:
- Cleanup completed successfully
- No manual action required
- Workspace can be safely deleted if not needed

EOF
        fi
    fi

    log_info "Cleanup report generated: $report_file"
}

# =============================================================================
# ARTIFACTS MANAGEMENT
# =============================================================================

create_cleanup_artifacts() {
    local cleanup_log="$1"

    log_info "Creating cleanup artifacts archive..."

    local artifacts=(
        "$cleanup_log"
        "$SHARED_VOLUME_PATH/pre-cleanup-state-list.txt"
        "$SHARED_VOLUME_PATH/pre-cleanup-outputs.json"
        "$SHARED_VOLUME_PATH/available-workspaces.txt"
        "$SHARED_VOLUME_PATH/remaining-resources.txt"
        "$SHARED_VOLUME_PATH/infrastructure-cleanup-report.txt"
    )

    # Create backup of Terraform state if it exists
    if [[ -f "terraform.tfstate" ]]; then
        backup_file "terraform.tfstate" "cleanup-$(date +%Y%m%d-%H%M%S)"
        artifacts+=("terraform.tfstate.backup-*")
    fi

    # Create the archive
    local archive_file="$SHARED_VOLUME_PATH/infrastructure-cleanup-artifacts.tar.gz"
    create_cleanup_archive "$archive_file" "${artifacts[@]}"
}

# =============================================================================
# HELP AND USAGE
# =============================================================================

show_help() {
    cat << EOF
Usage: $SCRIPT_NAME [OPTIONS]

Airgap Infrastructure Cleanup Script
This script consolidates infrastructure destruction, workspace management, and cleanup operations.

OPTIONS:
    -w, --workspace WORKSPACE    Terraform workspace name (default: \$TF_WORKSPACE)
    -v, --var-file FILE         Terraform variables file (default: \$TERRAFORM_VARS_FILENAME)
    -l, --local-path           Use local module path instead of remote
    --no-workspace-cleanup     Do not clean up the workspace after destruction
    -h, --help                 Show this help message
    --debug                   Enable debug logging

ENVIRONMENT VARIABLES:
    TF_WORKSPACE                  Terraform workspace name
    TERRAFORM_VARS_FILENAME       Terraform variables file name
    TERRAFORM_BACKEND_VARS_FILENAME Terraform backend variables file name
    QA_INFRA_WORK_PATH           Path to qa-infra-automation repository
    AWS_ACCESS_KEY_ID            AWS access key
    AWS_SECRET_ACCESS_KEY        AWS secret key
    AWS_REGION                   AWS region
    DEBUG                        Enable debug logging (true/false)

EXAMPLES:
    # Clean up default workspace
    $SCRIPT_NAME

    # Clean up specific workspace
    $SCRIPT_NAME -w my-workspace

    # Clean up without removing workspace
    $SCRIPT_NAME --no-workspace-cleanup

    # Clean up with debug logging
    DEBUG=true $SCRIPT_NAME --debug

EOF
}

# =============================================================================
# ARGUMENT PARSING
# =============================================================================

parse_arguments() {
    local workspace="$TF_WORKSPACE"
    local var_file="$TERRAFORM_VARS_FILENAME"
    local use_remote_path="true"
    local cleanup_workspace="true"

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
            --no-workspace-cleanup)
                cleanup_workspace="false"
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
    export CLEANUP_WORKSPACE="$cleanup_workspace"

    log_info "Configuration:"
    log_info "  Workspace: $workspace"
    log_info "  Variables file: $var_file"
    log_info "  Use remote path: $use_remote_path"
    log_info "  Clean up workspace: $cleanup_workspace"
    log_info "  Debug mode: ${DEBUG:-false}"
}

# =============================================================================
# MAIN SCRIPT EXECUTION
# =============================================================================

main() {
  log_info "Starting infrastructure cleanup with $SCRIPT_NAME"

  # Validate prerequisites
  validate_prerequisites

  # Parse command line arguments
  parse_arguments "$@"

  # Initialize the airgap environment
  initialize_airgap_environment

  # Wait for confirmation if in interactive mode
  wait_for_confirmation "Press Enter to start infrastructure cleanup..."

  # Run the cleanup
  cleanup_infrastructure "$TF_WORKSPACE" "$TERRAFORM_VARS_FILENAME" "$use_remote_path" "$CLEANUP_WORKSPACE"

  log_info "Infrastructure cleanup completed"
}

# Error handling
trap 'log_error "Script failed at line $LINENO"' ERR

# Execute main function with all arguments
main "$@"