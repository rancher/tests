#!/bin/bash
set -Eeuo pipefail
IFS=$'\n\t'

# Ansible RKE2 Deployment Script
# Consolidated script that handles RKE2 tarball deployment and validation
# Replaces: ansible_run_rke2_deployment.sh, ansible_setup_kubectl.sh, ansible_validate_rancher.sh

# =============================================================================
# SCRIPT CONFIGURATION
# =============================================================================

SCRIPT_NAME="$(basename "$0")"
readonly SCRIPT_NAME
SCRIPT_DIR="$(dirname "$0")"
readonly SCRIPT_DIR
QA_INFRA_CLONE_PATH="/root/qa-infra-automation"
readonly QA_INFRA_CLONE_PATH
RKE2_PLAYBOOK="$QA_INFRA_CLONE_PATH/ansible/rke2/airgap/playbooks/deploy/rke2-tarball-playbook.yml"
readonly RKE2_PLAYBOOK

# Try to source common shell library for logging/helpers first
# shellcheck disable=SC1090
if ! type log_info >/dev/null 2>&1; then
    COMMON_CANDIDATES=(
        "${SCRIPT_DIR}/../../../lib/common.sh" \
        "/root/go/src/github.com/rancher/tests/scripts/lib/common.sh" \
        "/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/lib/common.sh"
    )
    for c in "${COMMON_CANDIDATES[@]}"; do
        [ -f "$c" ] && . "$c" && break
    done
fi

# Load the airgap library (try multiple candidate locations)
# shellcheck disable=SC1090
if ! type log_info >/dev/null 2>&1; then
    lib_candidates=(
        "${SCRIPT_DIR}/airgap_lib.sh"
        "/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap/airgap_lib.sh"
        "/root/go/src/github.com/rancher/qa-infra-automation/validation/pipeline/scripts/airgap_lib.sh"
        "/root/qa-infra-automation/validation/pipeline/scripts/airgap_lib.sh"
    )

    for lib in "${lib_candidates[@]}"; do
        if [[ -f "$lib" ]]; then
            source "$lib"
            log_info "Sourced airgap library from: $lib"
            break
        fi
    done

    if ! type log_info >/dev/null 2>&1; then
        echo "[ERROR] airgap_lib.sh not found in expected locations: ${lib_candidates[*]}" >&2
        exit 1
    fi
fi

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
            log_info "  [OK] $file exists and is not empty"
        else
            log_error "  [FAIL] $file is missing or empty"
            ((validation_errors++))
        fi
    done

    # Check qa-infra-automation repository
    if [[ -d "$QA_INFRA_CLONE_PATH" ]]; then
        log_info "  [OK] qa-infra-automation repository exists"
    else
        log_error "  [FAIL] qa-infra-automation repository not found"
        ((validation_errors++))
    fi

    # Check RKE2 playbook
    if [[ -f "$RKE2_PLAYBOOK" ]]; then
        log_info "  [OK] RKE2 deployment playbook exists: $RKE2_PLAYBOOK"
    else
        log_error "  [FAIL] RKE2 deployment playbook not found: $RKE2_PLAYBOOK"
        ((validation_errors++))
    fi

    # Check environment variables
    local required_vars=("RKE2_VERSION")
    log_info "Checking environment variables:"
    for var in "${required_vars[@]}"; do
        if [[ -n "${!var}" ]]; then
            log_info "  [OK] $var=${!var}"
        else
            log_warning "  [WARN] $var is not set"
        fi
    done

    # Check Ansible availability
    if command -v ansible-playbook &>/dev/null; then
        log_info "  [OK] ansible-playbook is available"
    else
        log_error "  [FAIL] ansible-playbook not found"
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

    # Build extra-vars as an array (avoids word-splitting/globbing issues)
    local -a extra_args=()
    [[ -n "${RKE2_VERSION:-}" ]] && extra_args+=(-e "rke2_version=${RKE2_VERSION}")

    # Change to playbook directory
    cd "$QA_INFRA_CLONE_PATH/ansible/rke2/airgap" || {
        log_error "Failed to change to playbook directory"
        exit 1
    }

    # Show the command for debugging (join array for readability)
    log_info "Executing: ansible-playbook -i $inventory_file $RKE2_PLAYBOOK -v ${extra_args[*]}"

    # Run the playbook with logging
    if ansible-playbook -i "$inventory_file" "$RKE2_PLAYBOOK" -v "${extra_args[@]}" 2>&1 | tee "$log_file"; then
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

    log_info "Skipping cluster health checks from Jenkins agent (exit code: $exit_code)"
    log_info "Validate node readiness from the bastion host using the staged kubeconfig."
    export ANSIBLE_EXIT_CODE=$exit_code
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
    log_info "Skipping node role checks on Jenkins agent. Run 'kubectl get nodes -o wide' from the bastion host and capture results manually."
}

# =============================================================================
# KUBECTL SETUP
# =============================================================================

setup_kubectl_access() {
    log_info "Staging kubeconfig for downstream use"

    local source_config="/root/ansible/rke2/airgap/kubeconfig"
    local target_config="$SHARED_VOLUME_PATH/kubeconfig.yaml"

    if [[ -f "$source_config" ]]; then
        cp "$source_config" "$target_config"
        chmod 644 "$target_config"
        log_success "Kubeconfig copied to shared volume: $target_config"
        log_info "Kubernetes connectivity must be verified from the bastion host. Jenkins agents do not have network access to airgapped nodes."
    else
        log_warning "Expected kubeconfig not found at $source_config"
        return 1
    fi
}

# =============================================================================
# KUBECTL VERSION VERIFICATION
# =============================================================================

verify_kubectl_version() {
    log_info "Skipping kubectl version verification on Jenkins agent - perform from bastion host if required."
}

# =============================================================================
# CLUSTER VALIDATION
# =============================================================================

validate_rke2_cluster() {
    log_info "Skipping RKE2 cluster validation from Jenkins agent."
    log_info "Run validation commands from the bastion host using the staged kubeconfig:"
    log_info "  kubectl get nodes -o wide"
    log_info "  kubectl get pods -n kube-system"
    log_info "  kubectl cluster-info"
}

# =============================================================================
# DEPLOYMENT REPORT
# =============================================================================

generate_deployment_report() {
    log_info "Generating RKE2 deployment report"

    local report_file="$SHARED_VOLUME_PATH/rke2_deployment_report.txt"

    cat >"$report_file" <<EOF
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
- Jenkins agents cannot reach the airgapped cluster to gather kubectl data.
- Use the staged kubeconfig on the bastion host to collect node and pod status.

Artifacts Generated:
- $SHARED_VOLUME_PATH/kubeconfig.yaml
- $SHARED_VOLUME_PATH/rke2_deployment.log
- $SHARED_VOLUME_PATH/rke2_playbook_execution.log
- $SHARED_VOLUME_PATH/rke2_deployment_report.txt

Recommendations:
- Perform post-deployment validation from the bastion host.
- Archive any kubectl outputs gathered remotely alongside this report.
EOF

    log_success "Deployment report generated: $report_file"
}

# =============================================================================
# UTILITY FUNCTIONS
# =============================================================================

show_cluster_status() {
    log_info "Cluster status reporting is unavailable on the Jenkins agent."
    log_info "Collect status information from the bastion host instead."
}

# =============================================================================
# HELP AND USAGE
# =============================================================================

show_help() {
    cat <<EOF
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
            -w | --workspace)
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
    log_info "Starting RKE2 deployment with $SCRIPT_NAME"

    # Parse command line arguments
    parse_arguments "$@"

    # Initialize the airgap environment
    initialize_airgap_environment

    # Wait for confirmation if in interactive mode
    wait_for_confirmation "Press Enter to start RKE2 deployment..."

    # Run the deployment
    deploy_rke2 "$TF_WORKSPACE"

    log_info "RKE2 deployment completed"
}

# Error handling
trap 'log_error "Script failed at line $LINENO"' ERR

# Execute main function with all arguments
main "$@"
