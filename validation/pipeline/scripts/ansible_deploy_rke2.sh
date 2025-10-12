#!/bin/bash
set -e

# Ansible RKE2 Deployment Script
# Consolidated script that handles RKE2 tarball deployment and validation
# Replaces: ansible_run_rke2_deployment.sh, ansible_setup_kubectl.sh, ansible_validate_rancher.sh

# Load the airgap library
source "/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap_lib.sh"

# =============================================================================
# SCRIPT CONFIGURATION
# =============================================================================

readonly SCRIPT_NAME="$(basename "$0")"
readonly SCRIPT_DIR="$(dirname "$0")"
readonly QA_INFRA_CLONE_PATH="/root/qa-infra-automation"
readonly RKE2_PLAYBOOK="$QA_INFRA_CLONE_PATH/ansible/rke2/airgap/playbooks/deploy/rke2-tarball-playbook.yml"

# =============================================================================
# RKE2 DEPLOYMENT
# =============================================================================

deploy_rke2() {
    local workspace="${1:-$TF_WORKSPACE}"

    log_info "Starting RKE2 deployment for workspace: $workspace"

    # Validate environment
    validate_deployment_prerequisites

    # Validate Ansible inventory and group_vars
    validate_deployment_files

    # Run RKE2 deployment playbook
    run_rke2_playbook

    # Verify deployment success
    verify_rke2_deployment

    # Setup kubectl access
    setup_kubectl_access

    # Run post-deployment validation
    validate_rke2_cluster

    # Generate deployment report
    generate_deployment_report

    log_success "RKE2 deployment completed successfully"
}

# =============================================================================
# PREREQUISITE VALIDATION
# =============================================================================

validate_deployment_prerequisites() {
    log_info "Validating RKE2 deployment prerequisites"

    local validation_errors=0

    # Check required files
    local required_files=(
        "/root/ansible/rke2/airgap/inventory.yml"
        "/root/ansible/rke2/airgap/group_vars/all.yml"
        "/root/.ssh/authorized_keys"
    )

    log_info "Checking required deployment files:"
    for file in "${required_files[@]}"; do
        if [[ -f "$file" && -s "$file" ]]; then
            log_info "  ✓ $file exists and is not empty"
        else
            log_error "  ✗ $file is missing or empty"
            ((validation_errors++))
        fi
    done

    # Check qa-infra-automation repository
    if [[ -d "$QA_INFRA_CLONE_PATH" ]]; then
        log_info "  ✓ qa-infra-automation repository exists"
    else
        log_error "  ✗ qa-infra-automation repository not found"
        ((validation_errors++))
    fi

    # Check RKE2 playbook
    if [[ -f "$RKE2_PLAYBOOK" ]]; then
        log_info "  ✓ RKE2 deployment playbook exists: $RKE2_PLAYBOOK"
    else
        log_error "  ✗ RKE2 deployment playbook not found: $RKE2_PLAYBOOK"
        ((validation_errors++))
    fi

    # Check environment variables
    local required_vars=("RKE2_VERSION")
    log_info "Checking environment variables:"
    for var in "${required_vars[@]}"; do
        if [[ -n "${!var}" ]]; then
            log_info "  ✓ $var=${!var}"
        else
            log_warning "  ⚠ $var is not set"
        fi
    done

    # Check Ansible availability
    if command -v ansible-playbook &>/dev/null; then
        log_info "  ✓ ansible-playbook is available"
    else
        log_error "  ✗ ansible-playbook not found"
        ((validation_errors++))
    fi

    if [[ $validation_errors -gt 0 ]]; then
        log_error "Prerequisite validation failed with $validation_errors errors"
        exit 1
    fi

    log_success "Prerequisite validation passed"
}

# =============================================================================
# DEPLOYMENT FILE VALIDATION
# =============================================================================

validate_deployment_files() {
    log_info "Validating deployment files"

    # Validate inventory file
    local inventory_file="/root/ansible/rke2/airgap/inventory.yml"
    log_info "Validating inventory file: $inventory_file"

    if ! validate_yaml_syntax "$inventory_file"; then
        log_error "Inventory file validation failed"
        exit 1
    fi

    # Validate group_vars file
    local group_vars_file="/root/ansible/rke2/airgap/group_vars/all.yml"
    log_info "Validating group_vars file: $group_vars_file"

    if ! validate_yaml_syntax "$group_vars_file"; then
        log_error "Group_vars file validation failed"
        exit 1
    fi

    # Show key variables from group_vars
    log_info "=== Key Variables from group_vars ==="
    local key_vars=("rke2_version" "cluster_name" "tls_san")
    for var in "${key_vars[@]}"; do
        if grep -q "^$var:" "$group_vars_file"; then
            local value
            value=$(grep "^$var:" "$group_vars_file" | cut -d':' -f2- | xargs)
            log_info "  $var: $value"
        else
            log_warning "  $var: not found"
        fi
    done
    log_info "=== End Key Variables ==="

    log_success "Deployment files validation completed"
}

# =============================================================================
# PLAYBOOK EXECUTION
# =============================================================================

run_rke2_playbook() {
    log_info "Running RKE2 deployment playbook"

    local inventory_file="/root/ansible/rke2/airgap/inventory.yml"
    local group_vars_file="/root/ansible/rke2/airgap/group_vars/all.yml"
    local log_file="$SHARED_VOLUME_PATH/rke2_deployment.log"

    # Check for test mode
    if grep -q "test_force_failure: true" "$group_vars_file" 2>/dev/null; then
        log_warning "=== TEST MODE DETECTED ==="
        log_warning "test_force_failure: true found in group_vars"
        log_warning "This will force deployment failure to test cleanup procedures"
        log_warning "To disable, remove 'test_force_failure: true' from group_vars"
        log_warning "=== END TEST MODE ==="
        sleep 5
        exit 1
    fi

    # Prepare extra variables
    local extra_vars=""
    if [[ -n "${RKE2_VERSION}" ]]; then
        extra_vars="-e rke2_version=${RKE2_VERSION}"
        log_info "Passing RKE2_VERSION as extra variable: ${RKE2_VERSION}"
    fi

    # Change to playbook directory
    cd "$QA_INFRA_CLONE_PATH/ansible/rke2/airgap" || {
        log_error "Failed to change to playbook directory"
        exit 1
    }

    log_info "Executing: ansible-playbook -i $inventory_file $RKE2_PLAYBOOK -v $extra_vars"

    # Run the playbook with logging
    if ansible-playbook -i "$inventory_file" "$RKE2_PLAYBOOK" -v $extra_vars 2>&1 | tee "$log_file"; then
        log_success "RKE2 deployment playbook completed successfully"
        export ANSIBLE_EXIT_CODE=0
    else
        local exit_code=$?
        log_warning "RKE2 deployment playbook had issues (exit code: $exit_code)"
        export ANSIBLE_EXIT_CODE=$exit_code

        # Check if deployment actually succeeded despite warnings
        check_deployment_status "$exit_code"
    fi

    # Copy execution log to shared volume
    if [[ -f "ansible-playbook.log" ]]; then
        cp ansible-playbook.log "$SHARED_VOLUME_PATH/rke2_playbook_execution.log"
    fi
}

# =============================================================================
# DEPLOYMENT STATUS CHECK
# =============================================================================

check_deployment_status() {
    local exit_code=$1

    log_info "Checking RKE2 deployment status (exit code: $exit_code)"

    # Check if we have a working cluster despite warnings
    local kubeconfig_locations=(
        "/root/.kube/config"
        "/etc/rancher/rke2/rke2.yaml"
        "/root/ansible/rke2/airgap/kubeconfig"
    )

    local working_kubeconfig=""
    for config_path in "${kubeconfig_locations[@]}"; do
        if [[ -f "$config_path" ]]; then
            export KUBECONFIG="$config_path"
            if kubectl get nodes --no-headers 2>/dev/null | grep -q "Ready"; then
                working_kubeconfig="$config_path"
                log_success "Found working cluster with kubeconfig: $config_path"
                break
            fi
        fi
    done

    if [[ -n "$working_kubeconfig" ]]; then
        log_warning "Ansible had warnings but cluster is operational"
        log_info "Treating deployment as successful"
        export ANSIBLE_EXIT_CODE=0

        # Show cluster status
        show_cluster_status
    else
        log_error "RKE2 deployment failed and cluster is not operational"
        export ANSIBLE_EXIT_CODE=$exit_code
    fi
}

# =============================================================================
# DEPLOYMENT VERIFICATION
# =============================================================================

verify_rke2_deployment() {
    log_info "Verifying RKE2 deployment"

    if [[ "${ANSIBLE_EXIT_CODE}" -ne 0 ]]; then
        log_error "RKE2 deployment failed, skipping verification"
        return 1
    fi

    # Check for node role verification playbook
    local node_role_playbook="$QA_INFRA_CLONE_PATH/ansible/rke2/airgap/playbooks/debug/check-node-roles.yml"
    local inventory_file="/root/ansible/rke2/airgap/inventory.yml"

    if [[ -f "$node_role_playbook" ]]; then
        log_info "Running node role verification playbook..."
        cd "$QA_INFRA_CLONE_PATH/ansible/rke2/airgap" || {
            log_error "Failed to change to playbook directory"
            return 1
        }

        if ansible-playbook -i "$inventory_file" "$node_role_playbook" -v 2>&1 | tee "$SHARED_VOLUME_PATH/node_role_verification.log"; then
            log_success "Node role verification completed"
        else
            log_warning "Node role verification had issues"
        fi
    else
        log_info "Node role verification playbook not found, performing manual check..."
        manual_node_role_check
    fi

    log_success "RKE2 deployment verification completed"
}

# =============================================================================
# MANUAL NODE ROLE CHECK
# =============================================================================

manual_node_role_check() {
    log_info "Performing manual node role check"

    local kubeconfig_locations=(
        "/root/.kube/config"
        "/etc/rancher/rke2/rke2.yaml"
    )

    for config_path in "${kubeconfig_locations[@]}"; do
        if [[ -f "$config_path" ]]; then
            export KUBECONFIG="$config_path"
            log_info "Checking nodes with kubeconfig: $config_path"

            if kubectl get nodes -o wide 2>/dev/null | tee "$SHARED_VOLUME_PATH/manual_node_check.log"; then
                log_success "Manual node check completed"
                return 0
            else
                log_warning "Could not get nodes with kubeconfig: $config_path"
            fi
        fi
    done

    log_warning "Manual node role check failed - kubectl not accessible"
}

# =============================================================================
# KUBECTL SETUP
# =============================================================================

setup_kubectl_access() {
    log_info "Setting up kubectl access"

    local kubeconfig_locations=(
        "/root/.kube/config"
        "/etc/rancher/rke2/rke2.yaml"
        "/root/ansible/rke2/airgap/kubeconfig"
    )

    local target_config="$SHARED_VOLUME_PATH/kubeconfig.yaml"
    local config_found=false

    for config_path in "${kubeconfig_locations[@]}"; do
        if [[ -f "$config_path" ]]; then
            log_info "Found kubeconfig at: $config_path"

            # Copy to shared volume
            cp "$config_path" "$target_config"
            chmod 644 "$target_config"

            # Test the configuration
            export KUBECONFIG="$target_config"
            if kubectl cluster-info &>/dev/null; then
                log_success "Kubectl access configured successfully"
                log_info "Kubeconfig copied to: $target_config"
                config_found=true
                break
            else
                log_warning "Kubeconfig test failed for: $config_path"
            fi
        fi
    done

    if [[ "$config_found" == "false" ]]; then
        log_error "Could not setup kubectl access - no working kubeconfig found"
        return 1
    fi

    # Verify kubectl version compatibility
    verify_kubectl_version
}

# =============================================================================
# KUBECTL VERSION VERIFICATION
# =============================================================================

verify_kubectl_version() {
    log_info "Verifying kubectl version compatibility"

    if command -v kubectl &>/dev/null; then
        local kubectl_version
        kubectl_version=$(kubectl version --client --short 2>/dev/null | grep "Client Version" | cut -d':' -f2- | xargs || echo "unknown")
        log_info "kubectl version: $kubectl_version"

        # Check cluster version
        if kubectl version --short &>/dev/null; then
            local cluster_version
            cluster_version=$(kubectl version --short 2>/dev/null | grep "Server Version" | cut -d':' -f2- | xargs || echo "unknown")
            log_info "Cluster version: $cluster_version"
        else
            log_warning "Could not determine cluster version"
        fi
    else
        log_warning "kubectl command not found in PATH"
    fi
}

# =============================================================================
# CLUSTER VALIDATION
# =============================================================================

validate_rke2_cluster() {
    log_info "Validating RKE2 cluster"

    local validation_errors=0

    # Check node readiness
    log_info "Checking node readiness..."
    if kubectl get nodes --no-headers | grep -q "Ready"; then
        local ready_nodes
        ready_nodes=$(kubectl get nodes --no-headers | grep "Ready" | wc -l)
        local total_nodes
        total_nodes=$(kubectl get nodes --no-headers | wc -l)
        log_info "Node status: $ready_nodes/$total_nodes nodes ready"

        if [[ $ready_nodes -eq $total_nodes && $total_nodes -gt 0 ]]; then
            log_success "All nodes are ready"
        else
            log_warning "Some nodes are not ready"
            kubectl get nodes
        fi
    else
        log_error "No ready nodes found"
        ((validation_errors++))
    fi

    # Check system pods
    log_info "Checking system pods..."
    if kubectl get pods -n kube-system --no-headers | grep -q "Running"; then
        local running_pods
        running_pods=$(kubectl get pods -n kube-system --no-headers | grep "Running" | wc -l)
        local total_pods
        total_pods=$(kubectl get pods -n kube-system --no-headers | wc -l)
        log_info "System pods: $running_pods/$total_pods running"
    else
        log_warning "No running system pods found"
    fi

    # Check cluster info
    log_info "Cluster information:"
    if kubectl cluster-info 2>/dev/null; then
        log_success "Cluster is accessible"
    else
        log_warning "Cluster info command failed"
    fi

    if [[ $validation_errors -eq 0 ]]; then
        log_success "RKE2 cluster validation passed"
    else
        log_warning "RKE2 cluster validation had $validation_errors issues"
    fi
}

# =============================================================================
# DEPLOYMENT REPORT
# =============================================================================

generate_deployment_report() {
    log_info "Generating RKE2 deployment report"

    local report_file="$SHARED_VOLUME_PATH/rke2_deployment_report.txt"

    cat > "$report_file" << EOF
# RKE2 Deployment Report
# Generated on: $(date)
# Workspace: $TF_WORKSPACE
# Script: $SCRIPT_NAME

Deployment Summary:
- Start time: $(date)
- RKE2 Version: ${RKE2_VERSION:-not specified}
- Ansible Exit Code: ${ANSIBLE_EXIT_CODE:-not set}
- Working Directory: $(pwd)

Deployment Status:
EOF

    # Add node information
    if kubectl get nodes &>/dev/null; then
        echo "- Nodes:" >> "$report_file"
        kubectl get nodes >> "$report_file" 2>&1
    else
        echo "- Nodes: Not accessible" >> "$report_file"
    fi

    # Add pod information
    if kubectl get pods -n kube-system &>/dev/null; then
        echo "" >> "$report_file"
        echo "- System Pods:" >> "$report_file"
        kubectl get pods -n kube-system >> "$report_file" 2>&1
    fi

    cat >> "$report_file" << EOF

Artifacts Generated:
- $SHARED_VOLUME_PATH/kubeconfig.yaml
- $SHARED_VOLUME_PATH/rke2_deployment.log
- $SHARED_VOLUME_PATH/rke2_playbook_execution.log
- $SHARED_VOLUME_PATH/node_role_verification.log
- $SHARED_VOLUME_PATH/manual_node_check.log
- $SHARED_VOLUME_PATH/rke2_deployment_report.txt

EOF

    # Add recommendations
    if [[ "${ANSIBLE_EXIT_CODE}" -eq 0 ]]; then
        cat >> "$report_file" << EOF
Recommendations:
- RKE2 deployment completed successfully
- Kubeconfig is available for Rancher deployment
- Cluster is ready for next deployment phase
EOF
    else
        cat >> "$report_file" << EOF
Recommendations:
- RKE2 deployment had issues
- Check deployment logs for details
- Manual intervention may be required
- Consider running cleanup and redeploy
EOF
    fi

    log_success "Deployment report generated: $report_file"
}

# =============================================================================
# UTILITY FUNCTIONS
# =============================================================================

show_cluster_status() {
    log_info "=== Cluster Status ==="

    if kubectl get nodes &>/dev/null; then
        echo "Nodes:"
        kubectl get nodes
        echo ""
    fi

    if kubectl get pods -A &>/dev/null; then
        echo "Pods by namespace:"
        kubectl get pods -A
        echo ""
    fi

    echo "=== End Cluster Status ==="
}

# =============================================================================
# HELP AND USAGE
# =============================================================================

show_help() {
    cat << EOF
Usage: $SCRIPT_NAME [OPTIONS]

Ansible RKE2 Deployment Script
This script deploys RKE2 using the qa-infra-automation playbooks and validates the deployment.

OPTIONS:
    -w, --workspace WORKSPACE    Terraform workspace name (default: \$TF_WORKSPACE)
    -h, --help                 Show this help message
    --debug                   Enable debug logging
    --skip-validation         Skip post-deployment validation

ENVIRONMENT VARIABLES:
    TF_WORKSPACE              Terraform workspace name
    RKE2_VERSION             RKE2 version to deploy
    ANSIBLE_VARIABLES        Ansible variables content
    DEBUG                    Enable debug logging (true/false)

EXAMPLES:
    # Deploy RKE2 with default settings
    $SCRIPT_NAME

    # Deploy with specific workspace
    $SCRIPT_NAME -w my-workspace

    # Deploy with debug logging
    DEBUG=true $SCRIPT_NAME --debug

    # Deploy without validation
    $SCRIPT_NAME --skip-validation

EOF
}

# =============================================================================
# ARGUMENT PARSING
# =============================================================================

parse_arguments() {
    local workspace="$TF_WORKSPACE"
    local skip_validation="false"

    while [[ $# -gt 0 ]]; do
        case $1 in
            -w|--workspace)
                workspace="$2"
                shift 2
                ;;
            --skip-validation)
                skip_validation="true"
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

    export TF_WORKSPACE="$workspace"
    export SKIP_VALIDATION="$skip_validation"

    log_info "Configuration:"
    log_info "  Workspace: $workspace"
    log_info "  Skip validation: $skip_validation"
    log_info "  Debug mode: ${DEBUG:-false}"
}

# =============================================================================
# MAIN SCRIPT EXECUTION
# =============================================================================

main() {
    log_info "=== RKE2 Deployment Started ==="
    log_info "Script: $SCRIPT_NAME"
    log_info "Timestamp: $(date)"
    log_info "Working directory: $(pwd)"

    # Parse command line arguments
    parse_arguments "$@"

    # Initialize the airgap environment
    initialize_airgap_environment

    # Wait for confirmation if in interactive mode
    wait_for_confirmation "Press Enter to start RKE2 deployment..."

    # Run the deployment
    deploy_rke2 "$TF_WORKSPACE"

    log_success "=== RKE2 Deployment Completed ==="
}

# Error handling
trap 'log_error "Script failed at line $LINENO"' ERR

# Execute main function with all arguments
main "$@"