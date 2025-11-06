#!/bin/bash
set -Eeuo pipefail
IFS=$'\n\t'

# Airgap Unified Cleanup Script
# Consolidated script that handles all types of cleanup operations
# Replaces: airgap_deployment_failure_cleanup.sh, ansible_failure_cleanup.sh, airgap_timeout_cleanup.sh

# =============================================================================
# CONSTANTS
# =============================================================================

SCRIPT_NAME="$(basename "$0")"
readonly SCRIPT_NAME
SCRIPT_DIR="$(dirname "$0")"
readonly SCRIPT_DIR
QA_INFRA_CLONE_PATH="/root/qa-infra-automation"
readonly QA_INFRA_CLONE_PATH
CLEANUP_TYPE="${CLEANUP_TYPE:-deployment_failure}" # deployment_failure, timeout, manual
DESTROY_ON_FAILURE="${DESTROY_ON_FAILURE:-true}"
readonly DESTROY_ON_FAILURE

# =============================================================================
# LOGGING FUNCTIONS
# =============================================================================

# Logging functions will be provided by airgap_lib.sh

# =============================================================================
# PREREQUISITE VALIDATION
# =============================================================================

validate_prerequisites() {
    # If logging helper already exists, assume library is loaded
    if type log_info >/dev/null 2>&1; then
        return 0
    fi

    # Prefer the new common shell library; fall back to legacy airgap_lib.sh locations
    local lib_candidates=(
        "${SCRIPT_DIR}/../../../../scripts/lib/common.sh"
        "${SCRIPT_DIR}/../../../scripts/lib/common.sh"
        "${SCRIPT_DIR}/../scripts/lib/common.sh"
        "${SCRIPT_DIR}/airgap_lib.sh"
        "/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap/airgap_lib.sh"
        "/root/go/src/github.com/rancher/qa-infra-automation/validation/pipeline/scripts/airgap_lib.sh"
        "/root/qa-infra-automation/validation/pipeline/scripts/airgap_lib.sh"
    )

    for lib in "${lib_candidates[@]}"; do
        if [[ -f "$lib" ]]; then
            # shellcheck disable=SC1090
            source "$lib"
            # Prefer the generic log_info if present, otherwise continue searching
            if type log_info >/dev/null 2>&1; then
                log_info "Sourced common/airgap library from: $lib"
                break
            fi
        fi
    done

    if ! type log_info >/dev/null 2>&1; then
        echo "[ERROR] common/airgap library not found in expected locations: ${lib_candidates[*]}" >&2
        exit 1
    fi

    # Ensure required tools are available
    command -v ansible-playbook >/dev/null || {
        log_error "ansible-playbook not found"
        exit 1
    }
}

# =============================================================================
# SCRIPT CONFIGURATION
# =============================================================================

# Load the airgap library (robust sourcing is handled by validate_prerequisites)

# =============================================================================
# UNIFIED CLEANUP MAIN FUNCTION
# =============================================================================

perform_cleanup() {
    local cleanup_type="${1:-$CLEANUP_TYPE}"
    local workspace="${2:-$TF_WORKSPACE}"
    local destroy_infrastructure="${3:-$DESTROY_ON_FAILURE}"

    log_info "Starting unified cleanup for workspace: $workspace"
    log_info "Cleanup type: $cleanup_type"
    log_info "Destroy infrastructure: $destroy_infrastructure"

    # Initialize cleanup environment
    initialize_cleanup_environment "$cleanup_type" "$workspace"

    # Gather current state information
    gather_cleanup_state "$cleanup_type" "$workspace"

    # Perform cleanup based on type
    case "$cleanup_type" in
        "deployment_failure")
            handle_deployment_failure_cleanup "$workspace" "$destroy_infrastructure"
            ;;
        "timeout")
            handle_timeout_cleanup "$workspace" "$destroy_infrastructure"
            ;;
        "manual")
            handle_manual_cleanup "$workspace" "$destroy_infrastructure"
            ;;
        *)
            log_error "Unknown cleanup type: $cleanup_type"
            exit 1
            ;;
    esac

    # Generate cleanup report
    generate_unified_cleanup_report "$cleanup_type" "$workspace" "$destroy_infrastructure"

    # Create cleanup artifacts
    create_unified_cleanup_artifacts

    log_success "Unified cleanup completed"
}

# =============================================================================
# CLEANUP ENVIRONMENT INITIALIZATION
# =============================================================================

initialize_cleanup_environment() {
    local cleanup_type="$1"
    local workspace="$2"

    log_info "Initializing cleanup environment for: $cleanup_type"

    # Create cleanup log
    local cleanup_log="$SHARED_VOLUME_PATH/unified-cleanup.log"
    cat >"$cleanup_log" <<EOF
# Unified Cleanup Log
# Generated on: $(date)
# Cleanup type: $cleanup_type
# Workspace: $workspace
# DESTROY_ON_FAILURE: $DESTROY_ON_FAILURE

Cleanup operations performed:
EOF

    export UNIFIED_CLEANUP_LOG="$cleanup_log"

    # Initialize airgap environment
    initialize_airgap_environment

    log_success "Cleanup environment initialized"
}

# =============================================================================
# STATE GATHERING
# =============================================================================

gather_cleanup_state() {
    local cleanup_type="$1"
    local workspace="$2"

    log_cleanup "Gathering current state for $cleanup_type cleanup"

    # Get Terraform workspace information
    if [[ -n "$TF_WORKSPACE" ]]; then
        cd "$TOFU_MODULE_PATH" || {
            log_cleanup "WARNING: Could not change to Terraform module directory"
            return 1
        }

        # For deployment failures, workspace may not exist yet - that's ok
        local allow_missing="false"
        if [[ "$cleanup_type" == "deployment_failure" ]]; then
            allow_missing="true"
            log_cleanup "Deployment failure cleanup - workspace may not exist yet"
        fi

        # Initialize to access state
        local init_result=0
        initialize_tofu "$TOFU_MODULE_PATH" "$allow_missing" || init_result=$?
        
        if [[ $init_result -eq 0 ]]; then
            log_cleanup "Terraform initialized successfully"

            # Get current workspace
            local current_workspace
            current_workspace=$(tofu workspace show 2>/dev/null || echo "unknown")
            log_cleanup "Current Terraform workspace: $current_workspace"

            # Get resource list
            log_cleanup "Extracting resource list from Terraform state..."
            if tofu state list >"$SHARED_VOLUME_PATH/pre-cleanup-state-list.txt" 2>/dev/null; then
                local resource_count
                resource_count=$(wc -l <"$SHARED_VOLUME_PATH/pre-cleanup-state-list.txt")
                log_cleanup "Found $resource_count resources in state"
            else
                log_cleanup "Failed to extract resource list (state may be empty)"
            fi

            # Get outputs
            log_cleanup "Extracting Terraform outputs..."
            if tofu output -json >"$SHARED_VOLUME_PATH/pre-cleanup-outputs.json" 2>/dev/null; then
                log_cleanup "Terraform outputs extracted successfully"
            else
                log_cleanup "Failed to extract Terraform outputs"
            fi
        elif [[ $init_result -eq 2 ]]; then
            log_cleanup "No state found - workspace was never created (return code 2)"
            log_cleanup "This is expected for early deployment failures"
            # Create empty state list to indicate no resources
            echo "" > "$SHARED_VOLUME_PATH/pre-cleanup-state-list.txt"
        else
            log_cleanup "WARNING: Terraform initialization failed during state gathering (return code $init_result)"
        fi
    fi

    # Gather Ansible/Kubernetes information if available
    gather_kubernetes_state "$cleanup_type"

    # Gather system information
    gather_system_state "$cleanup_type"

    log_cleanup "State gathering completed"
}

# =============================================================================
# KUBERNETES STATE GATHERING
# =============================================================================

gather_kubernetes_state() {
    local cleanup_type="$1"

    log_cleanup "Gathering Kubernetes state"

    # Check for kubeconfig files
    local kubeconfig_locations=(
        "$SHARED_VOLUME_PATH/kubeconfig.yaml"
        "/root/.kube/config"
        "/etc/rancher/rke2/rke2.yaml"
    )

    local kubeconfig_found=false
    for config_path in "${kubeconfig_locations[@]}"; do
        if [[ -f "$config_path" ]]; then
            export KUBECONFIG="$config_path"
            if kubectl cluster-info &>/dev/null; then
                log_cleanup "Found working kubeconfig: $config_path"
                kubeconfig_found=true
                break
            fi
        fi
    done

    if [[ "$kubeconfig_found" == "true" ]]; then
        # Get node information
        log_cleanup "Extracting Kubernetes node information..."
        if kubectl get nodes -o wide >"$SHARED_VOLUME_PATH/pre-cleanup-nodes.txt" 2>/dev/null; then
            local node_count
            node_count=$(grep -c "Ready" "$SHARED_VOLUME_PATH/pre-cleanup-nodes.txt" 2>/dev/null || echo "0")
            log_cleanup "Found $node_count ready nodes"
        fi

        # Get pod information
        log_cleanup "Extracting Kubernetes pod information..."
        if kubectl get pods -A >"$SHARED_VOLUME_PATH/pre-cleanup-pods.txt" 2>/dev/null; then
            local pod_count
            pod_count=$(wc -l <"$SHARED_VOLUME_PATH/pre-cleanup-pods.txt")
            log_cleanup "Found $pod_count total pods across all namespaces"
        fi

        # Get namespace information (especially Rancher namespaces)
        log_cleanup "Checking for Rancher namespaces..."
        if kubectl get namespaces | grep -E "(cattle|rancher)" >"$SHARED_VOLUME_PATH/pre-cleanup-rancher-namespaces.txt" 2>/dev/null; then
            log_cleanup "Found Rancher-related namespaces"
        fi
    else
        log_cleanup "No working kubeconfig found, skipping Kubernetes state gathering"
    fi
}

# =============================================================================
# SYSTEM STATE GATHERING
# =============================================================================

gather_system_state() {
    local cleanup_type="$1"

    log_cleanup "Gathering system state information"

    # Get disk usage
    log_cleanup "Recording disk usage..."
    df -h >"$SHARED_VOLUME_PATH/pre-cleanup-disk-usage.txt" 2>/dev/null || true

    # Get memory usage
    log_cleanup "Recording memory usage..."
    free -h >"$SHARED_VOLUME_PATH/pre-cleanup-memory-usage.txt" 2>/dev/null || true

    # Get running processes
    log_cleanup "Recording relevant processes..."
    # Prefer pgrep to avoid parsing ps output
    pgrep -af "tofu|terraform|ansible|kubectl" >"$SHARED_VOLUME_PATH/pre-cleanup-processes.txt" 2>/dev/null || true

    # List files in shared volume
    log_cleanup "Listing shared volume contents..."
    ls -la "$SHARED_VOLUME_PATH" >"$SHARED_VOLUME_PATH/pre-cleanup-shared-volume.txt" 2>/dev/null || true

    log_cleanup "System state gathering completed"
}

# =============================================================================
# DEPLOYMENT FAILURE CLEANUP
# =============================================================================

handle_deployment_failure_cleanup() {
    local workspace="$1"
    local destroy_infrastructure="$2"

    log_cleanup "=== Handling Deployment Failure Cleanup ==="

    # Check if this was an Ansible failure
    if [[ -f "$SHARED_VOLUME_PATH/ansible-failure-summary.txt" ]]; then
        log_cleanup "Detected Ansible deployment failure"
        handle_ansible_failure_specific_cleanup "$workspace" "$destroy_infrastructure"
    fi

    # Check if this was an infrastructure failure
    if [[ -f "$SHARED_VOLUME_PATH/infrastructure-outputs.json" ]] && [[ ! -f "$SHARED_VOLUME_PATH/kubeconfig.yaml" ]]; then
        log_cleanup "Detected infrastructure deployment failure"
        handle_infrastructure_failure_specific_cleanup "$workspace" "$destroy_infrastructure"
    fi

    # General deployment failure cleanup
    handle_general_deployment_failure_cleanup "$workspace" "$destroy_infrastructure"

    log_cleanup "=== Deployment Failure Cleanup Completed ==="
}

# =============================================================================
# ANSIBLE FAILURE SPECIFIC CLEANUP
# =============================================================================

handle_ansible_failure_specific_cleanup() {
    local workspace="$1"
    local destroy_infrastructure="$2"

    log_cleanup "Performing Ansible failure specific cleanup"

    # Check for Ansible log files
    local ansible_logs=(
        "$SHARED_VOLUME_PATH/rke2_deployment.log"
        "$SHARED_VOLUME_PATH/rancher_deployment.log"
        "$SHARED_VOLUME_PATH/ansible_playbook_execution.log"
    )

    log_cleanup "Analyzing Ansible failure logs..."
    for log_file in "${ansible_logs[@]}"; do
        if [[ -f "$log_file" ]]; then
            log_cleanup "Found Ansible log: $log_file"
            # Extract error information
            if grep -i "error\|failed\|timeout" "$log_file" | head -10 >>"$UNIFIED_CLEANUP_LOG"; then
                log_cleanup "Extracted error information from $log_file"
            fi
        fi
    done

    # Check for partial Kubernetes deployment
    if kubectl cluster-info &>/dev/null; then
        log_cleanup "Partial Kubernetes deployment detected, checking for cleanup..."
        # Note: We don't automatically clean up Kubernetes resources as it might be recoverable
        log_cleanup "Kubernetes cluster is accessible - manual review may be needed"
    fi

    log_cleanup "Ansible failure specific cleanup completed"
}

# =============================================================================
# INFRASTRUCTURE FAILURE SPECIFIC CLEANUP
# =============================================================================

handle_infrastructure_failure_specific_cleanup() {
    local workspace="$1"
    local destroy_infrastructure="$2"

    log_cleanup "Performing infrastructure failure specific cleanup"

    # Check for infrastructure outputs
    if [[ -f "$SHARED_VOLUME_PATH/infrastructure-outputs.json" ]]; then
        log_cleanup "Infrastructure outputs found, analyzing deployment state..."

        # Check if any resources were created
        local state_list_file="$SHARED_VOLUME_PATH/pre-cleanup-state-list.txt"
        if [[ -f "$state_list_file" ]]; then
            local resource_count
            resource_count=$(wc -l <"$state_list_file")
            if [[ $resource_count -gt 1 ]]; then
                log_cleanup "Partial infrastructure deployment detected ($resource_count resources)"
                log_cleanup "Infrastructure cleanup will be required"
            else
                log_cleanup "No infrastructure resources created"
            fi
        fi
    fi

    log_cleanup "Infrastructure failure specific cleanup completed"
}

# =============================================================================
# GENERAL DEPLOYMENT FAILURE CLEANUP
# =============================================================================

handle_general_deployment_failure_cleanup() {
    local workspace="$1"
    local destroy_infrastructure="$2"

    log_cleanup "Performing general deployment failure cleanup"

    # Clean up temporary files
    log_cleanup "Cleaning up temporary files..."
    find /tmp -name "*airgap*" -type f -delete 2>/dev/null || true
    find /tmp -name "*terraform*" -type f -delete 2>/dev/null || true
    find /tmp -name "*ansible*" -type f -delete 2>/dev/null || true

    # Clean up log files if they're too large
    log_cleanup "Managing log file sizes..."
    find "$SHARED_VOLUME_PATH" -name "*.log" -size +100M -exec truncate -s 50M {} \; 2>/dev/null || true

    # Perform infrastructure destruction if requested
    if [[ "$destroy_infrastructure" == "true" ]]; then
        log_cleanup "DESTROY_ON_FAILURE enabled - performing infrastructure destruction..."
        perform_infrastructure_destruction "$workspace"
    else
        log_cleanup "DESTROY_ON_FAILURE disabled - preserving infrastructure"
        log_cleanup "Manual cleanup will be required for workspace: $workspace"
    fi

    log_cleanup "General deployment failure cleanup completed"
}

# =============================================================================
# TIMEOUT CLEANUP
# =============================================================================

handle_timeout_cleanup() {
    local workspace="$1"
    local destroy_infrastructure="$2"

    log_cleanup "=== Handling Timeout Cleanup ==="

    log_cleanup "Timeout occurred during deployment - analyzing state..."

    # Check what stage the timeout occurred at
    if [[ -f "$SHARED_VOLUME_PATH/infrastructure-outputs.json" ]]; then
        log_cleanup "Infrastructure deployment completed before timeout"
    else
        log_cleanup "Infrastructure deployment may have failed or timed out"
    fi

    if [[ -f "$SHARED_VOLUME_PATH/kubeconfig.yaml" ]]; then
        log_cleanup "Kubernetes cluster was provisioned before timeout"
    else
        log_cleanup "Kubernetes cluster was not provisioned before timeout"
    fi

    # Check for long-running processes
    log_cleanup "Checking for long-running processes that may have caused timeout..."
    if pgrep -f "ansible-playbook|tofu" >/dev/null 2>&1; then
        log_cleanup "Found long-running processes - attempting to terminate..."
        pkill -f "ansible-playbook" 2>/dev/null || true
        pkill -f "tofu.*apply" 2>/dev/null || true
    fi

    # Perform infrastructure cleanup if requested
    if [[ "$destroy_infrastructure" == "true" ]]; then
        log_cleanup "Performing infrastructure destruction due to timeout..."
        perform_infrastructure_destruction "$workspace"
    else
        log_cleanup "Preserving infrastructure for manual timeout analysis"
    fi

    log_cleanup "=== Timeout Cleanup Completed ==="
}

# =============================================================================
# MANUAL CLEANUP
# =============================================================================

handle_manual_cleanup() {
    local workspace="$1"
    local destroy_infrastructure="$2"

    log_cleanup "=== Handling Manual Cleanup ==="

    log_cleanup "Manual cleanup initiated - preserving state for analysis..."

    # Create comprehensive backup of current state
    log_cleanup "Creating comprehensive state backup..."

    local backup_dir
    backup_dir="$SHARED_VOLUME_PATH/manual-cleanup-backup-$(date +%Y%m%d-%H%M%S)"
    mkdir -p "$backup_dir"

    # Backup important files
    local backup_files=(
        "$SHARED_VOLUME_PATH/infrastructure-outputs.json"
        "$SHARED_VOLUME_PATH/kubeconfig.yaml"
        "$SHARED_VOLUME_PATH/pre-cleanup-state-list.txt"
        "$SHARED_VOLUME_PATH/pre-cleanup-outputs.json"
        "$SHARED_VOLUME_PATH/pre-cleanup-nodes.txt"
        "$SHARED_VOLUME_PATH/pre-cleanup-pods.txt"
        "/root/ansible/rke2/airgap/group_vars/all.yml"
        "/root/ansible/rke2/airgap/inventory.yml"
    )

    for file in "${backup_files[@]}"; do
        if [[ -f "$file" ]]; then
            cp "$file" "$backup_dir/" 2>/dev/null || true
        fi
    done

    log_cleanup "Manual cleanup backup created: $backup_dir"

    # Ask for confirmation before destroying infrastructure
    if [[ "$destroy_infrastructure" == "true" ]]; then
        if [[ "${INTERACTIVE:-false}" == "true" ]]; then
            echo -n "Destroy infrastructure for workspace $workspace? (y/N): "
            read -r response
            if [[ "$response" =~ ^[Yy]$ ]]; then
                perform_infrastructure_destruction "$workspace"
            else
                log_cleanup "Infrastructure preservation confirmed by user"
            fi
        else
            log_cleanup "Non-interactive mode - performing infrastructure destruction..."
            perform_infrastructure_destruction "$workspace"
        fi
    else
        log_cleanup "Infrastructure preservation requested"
    fi

    log_cleanup "=== Manual Cleanup Completed ==="
}

# =============================================================================
# INFRASTRUCTURE DESTRUCTION
# =============================================================================

perform_infrastructure_destruction() {
    local workspace="$1"

    log_cleanup "=== Starting Infrastructure Destruction ==="

    # Use the infrastructure cleanup script
    local cleanup_script="$SCRIPT_DIR/airgap_infrastructure_cleanup.sh"
    if [[ -f "$cleanup_script" ]]; then
        log_cleanup "Using infrastructure cleanup script: $cleanup_script"

        # Set environment variables for the cleanup script
        export TF_WORKSPACE="$workspace"
        export CLEANUP_WORKSPACE="true"

        # Run the cleanup script
        if bash "$cleanup_script" --local-path; then
            log_cleanup "[OK] Infrastructure destruction completed successfully"
        else
            log_cleanup "[FAIL] Infrastructure destruction failed or had issues"
        fi
    else
        log_cleanup "Infrastructure cleanup script not found, attempting manual destruction..."
        manual_infrastructure_destruction "$workspace"
    fi

    log_cleanup "=== Infrastructure Destruction Completed ==="
}

# =============================================================================
# MANUAL INFRASTRUCTURE DESTRUCTION
# =============================================================================

manual_infrastructure_destruction() {
    local workspace="$1"

    log_cleanup "Performing manual infrastructure destruction"

    cd "$TOFU_MODULE_PATH" || {
        log_cleanup "Failed to change to Terraform module directory"
        return 1
    }

    # Check if state exists first
    local state_list_file="$SHARED_VOLUME_PATH/pre-cleanup-state-list.txt"
    if [[ -f "$state_list_file" ]]; then
        local resource_count
        resource_count=$(wc -l <"$state_list_file" | tr -d ' ')
        if [[ $resource_count -eq 0 ]]; then
            log_cleanup "No resources found in state - skipping destruction"
            log_cleanup "Workspace may have never been created or was already cleaned up"
            return 0
        fi
        log_cleanup "State contains $resource_count resources to destroy"
    fi

    # Initialize Terraform (allow missing workspace for deployment failures)
    local init_result=0
    initialize_tofu "$TOFU_MODULE_PATH" "true" || init_result=$?
    
    if [[ $init_result -eq 2 ]]; then
        log_cleanup "Workspace never existed - no infrastructure to destroy"
        return 0
    elif [[ $init_result -ne 0 ]]; then
        log_cleanup "Failed to initialize Terraform for manual destruction"
        return 1
    fi

    log_cleanup "Terraform initialized for manual destruction"

    # Verify we have resources to destroy
    local current_resources=0
    if tofu state list >"$SHARED_VOLUME_PATH/current-state-list.txt" 2>/dev/null; then
        current_resources=$(wc -l <"$SHARED_VOLUME_PATH/current-state-list.txt" | tr -d ' ')
        log_cleanup "Current state contains $current_resources resources"
        
        if [[ $current_resources -eq 0 ]]; then
            log_cleanup "No resources to destroy in current state"
            return 0
        fi
    else
        log_cleanup "Failed to get state list - cannot verify resources"
    fi

    # Attempt destruction
    local var_file_arg=""
    if [[ -n "$TERRAFORM_VARS_FILENAME" && -f "$TERRAFORM_VARS_FILENAME" ]]; then
        var_file_arg="-var-file=$TERRAFORM_VARS_FILENAME"
        log_cleanup "Using var file: $TERRAFORM_VARS_FILENAME"
    fi
    
    if tofu destroy -auto-approve "$var_file_arg" 2>&1 | tee "$SHARED_VOLUME_PATH/destruction-output.log"; then
        log_cleanup "[OK] Manual infrastructure destruction completed"
    else
        log_cleanup "[FAIL] Manual infrastructure destruction failed"
        log_cleanup "Check destruction-output.log for details"
    fi
}

# =============================================================================
# REPORT GENERATION
# =============================================================================

generate_unified_cleanup_report() {
    local cleanup_type="$1"
    local workspace="$2"
    local destroy_infrastructure="$3"

    local report_file="$SHARED_VOLUME_PATH/unified-cleanup-report.txt"

    cat >"$report_file" <<EOF
# Unified Cleanup Report
# Generated on: $(date)
# Cleanup type: $cleanup_type
# Workspace: $workspace
# DESTROY_ON_FAILURE: $destroy_infrastructure

Cleanup Summary:
- Start time: $(date)
- Cleanup type: $cleanup_type
- Workspace: $workspace
- Script: $SCRIPT_NAME

Pre-cleanup State:
EOF

    # Add state information
    if [[ -f "$SHARED_VOLUME_PATH/pre-cleanup-state-list.txt" ]]; then
        local resource_count
        resource_count=$(wc -l <"$SHARED_VOLUME_PATH/pre-cleanup-state-list.txt")
        echo "- Terraform resources: $resource_count" >>"$report_file"
    fi

    if [[ -f "$SHARED_VOLUME_PATH/pre-cleanup-nodes.txt" ]]; then
        local node_count
        node_count=$(grep -c "Ready" "$SHARED_VOLUME_PATH/pre-cleanup-nodes.txt" 2>/dev/null || echo "0")
        echo "- Kubernetes nodes: $node_count ready" >>"$report_file"
    fi

    if [[ -f "$SHARED_VOLUME_PATH/pre-cleanup-pods.txt" ]]; then
        local pod_count
        pod_count=$(wc -l <"$SHARED_VOLUME_PATH/pre-cleanup-pods.txt")
        echo "- Kubernetes pods: $pod_count total" >>"$report_file"
    fi

    cat >>"$report_file" <<EOF

Cleanup Actions Taken:
1. Gathered current infrastructure and Kubernetes state
2. Analyzed failure type and specific cleanup requirements
3. ${destroy_infrastructure:+Performed infrastructure destruction}${destroy_infrastructure:-:Preserved infrastructure for manual cleanup}
4. Generated comprehensive cleanup artifacts

Post-cleanup Status:
EOF

    # Check post-cleanup state
    if [[ -f "$SHARED_VOLUME_PATH/remaining-resources.txt" ]]; then
        local remaining_count
        remaining_count=$(wc -l <"$SHARED_VOLUME_PATH/remaining-resources.txt")
        if [[ $remaining_count -eq 0 ]]; then
            echo "- Remaining infrastructure resources: None" >>"$report_file"
        else
            echo "- Remaining infrastructure resources: $remaining_count" >>"$report_file"
        fi
    fi

    cat >>"$report_file" <<EOF

Artifacts Generated:
- $UNIFIED_CLEANUP_LOG
- $SHARED_VOLUME_PATH/unified-cleanup-report.txt
- $SHARED_VOLUME_PATH/unified-cleanup-artifacts.tar.gz
- Various pre-cleanup state files

EOF

    # Add specific recommendations based on cleanup type
    case "$cleanup_type" in
        "deployment_failure")
            cat >>"$report_file" <<EOF
Deployment Failure Recommendations:
- Review deployment logs to identify root cause
- Check resource availability and connectivity
- Verify configuration files and parameters
- Consider redeployment after issue resolution
EOF
            ;;
        "timeout")
            cat >>"$report_file" <<EOF
Timeout Recommendations:
- Increase timeout values for long-running operations
- Check network connectivity and resource availability
- Consider breaking deployment into smaller stages
- Monitor system resources during deployment
EOF
            ;;
        "manual")
            cat >>"$report_file" <<EOF
Manual Cleanup Recommendations:
- Review backup files created during cleanup
- Analyze failure logs and system state
- Plan appropriate recovery strategy
- Document lessons learned for future deployments
EOF
            ;;
    esac

    log_cleanup "Unified cleanup report generated: $report_file"
}

# =============================================================================
# ARTIFACTS CREATION
# =============================================================================

create_unified_cleanup_artifacts() {
    log_cleanup "Creating unified cleanup artifacts archive"

    local artifacts=(
        "$UNIFIED_CLEANUP_LOG"
        "$SHARED_VOLUME_PATH/unified-cleanup-report.txt"
        "$SHARED_VOLUME_PATH/pre-cleanup-state-list.txt"
        "$SHARED_VOLUME_PATH/pre-cleanup-outputs.json"
        "$SHARED_VOLUME_PATH/pre-cleanup-nodes.txt"
        "$SHARED_VOLUME_PATH/pre-cleanup-pods.txt"
        "$SHARED_VOLUME_PATH/pre-cleanup-disk-usage.txt"
        "$SHARED_VOLUME_PATH/pre-cleanup-memory-usage.txt"
        "$SHARED_VOLUME_PATH/pre-cleanup-processes.txt"
        "$SHARED_VOLUME_PATH/remaining-resources.txt"
    )

    # Add deployment-specific logs if they exist
    local deployment_logs=(
        "$SHARED_VOLUME_PATH/rke2_deployment.log"
        "$SHARED_VOLUME_PATH/rancher_deployment.log"
        "$SHARED_VOLUME_PATH/ansible_playbook_execution.log"
        "$SHARED_VOLUME_PATH/rke2_deployment_report.txt"
        "$SHARED_VOLUME_PATH/rancher_deployment_report.txt"
    )

    artifacts+=("${deployment_logs[@]}")

    # Create the archive
    local archive_file="$SHARED_VOLUME_PATH/unified-cleanup-artifacts.tar.gz"
    create_cleanup_archive "$archive_file" "${artifacts[@]}"

    log_cleanup "Unified cleanup artifacts created: $archive_file"
}

# =============================================================================
# UTILITY FUNCTIONS
# =============================================================================

log_cleanup() {
    local message="$1"
    echo "[$(date)] $message" >>"$UNIFIED_CLEANUP_LOG"
    log_info "$message"
}

# =============================================================================
# HELP AND USAGE
# =============================================================================

show_help() {
    cat <<EOF
Usage: $SCRIPT_NAME [OPTIONS]

Unified Airgap Cleanup Script
This script handles cleanup operations for various failure scenarios in airgap deployments.

CLEANUP TYPES:
    deployment_failure    Clean up after deployment failure (default)
    timeout              Clean up after deployment timeout
    manual               Manual cleanup with state preservation

OPTIONS:
    -t, --type TYPE           Cleanup type (deployment_failure, timeout, manual)
    -w, --workspace WORKSPACE  Terraform workspace name (default: \$TF_WORKSPACE)
    -d, --destroy             Force infrastructure destruction
    --preserve-infrastructure  Preserve infrastructure (don't destroy)
    -h, --help                Show this help message
    --debug                   Enable debug logging
    --interactive             Interactive mode with confirmations

ENVIRONMENT VARIABLES:
    CLEANUP_TYPE             Type of cleanup to perform
    DESTROY_ON_FAILURE       Destroy infrastructure on failure (true/false)
    TF_WORKSPACE             Terraform workspace name
    DEBUG                    Enable debug logging (true/false)
    INTERACTIVE               Interactive mode (true/false)

EXAMPLES:
    # Cleanup deployment failure (default behavior)
    $SCRIPT_NAME

    # Cleanup timeout with infrastructure destruction
    $SCRIPT_NAME -t timeout -d

    # Manual cleanup preserving infrastructure
    $SCRIPT_NAME -t manual --preserve-infrastructure

    # Interactive cleanup for specific workspace
    INTERACTIVE=true $SCRIPT_NAME -w my-workspace --interactive

    # Cleanup with debug logging
    DEBUG=true $SCRIPT_NAME --debug

EOF
}

# =============================================================================
# ARGUMENT PARSING
# =============================================================================

parse_arguments() {
    local cleanup_type="$CLEANUP_TYPE"
    local workspace="$TF_WORKSPACE"
    local destroy_infrastructure="$DESTROY_ON_FAILURE"

    while [[ $# -gt 0 ]]; do
        case $1 in
            -t | --type)
                cleanup_type="$2"
                shift 2
                ;;
            -w | --workspace)
                workspace="$2"
                shift 2
                ;;
            -d | --destroy)
                destroy_infrastructure="true"
                shift
                ;;
            --preserve-infrastructure)
                destroy_infrastructure="false"
                shift
                ;;
            --interactive)
                export INTERACTIVE="true"
                shift
                ;;
            --debug)
                export DEBUG="true"
                shift
                ;;
            -h | --help)
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

    export CLEANUP_TYPE="$cleanup_type"
    export TF_WORKSPACE="$workspace"
    # Note: DESTROY_ON_FAILURE is already readonly from line 19, don't re-export

    log_info "Configuration:"
    log_info "  Cleanup type: $cleanup_type"
    log_info "  Workspace: $workspace"
    log_info "  Destroy infrastructure: $destroy_infrastructure"
    log_info "  Interactive mode: ${INTERACTIVE:-false}"
    log_info "  Debug mode: ${DEBUG:-false}"
}

# =============================================================================
# MAIN SCRIPT EXECUTION
# =============================================================================

main() {
    # Validate prerequisites
    validate_prerequisites

    log_info "Starting unified cleanup with $SCRIPT_NAME"

    # Parse command line arguments
    parse_arguments "$@"

    # Initialize the airgap environment
    initialize_airgap_environment

    # Wait for confirmation if in interactive mode
    wait_for_confirmation "Press Enter to start unified cleanup..."

    # Run the cleanup
    perform_cleanup "$CLEANUP_TYPE" "$TF_WORKSPACE" "$DESTROY_ON_FAILURE"

    log_info "Unified cleanup completed"
}

# Error handling
trap 'log_error "Script failed at line $LINENO"' ERR

# Execute main function with all arguments
main "$@"
