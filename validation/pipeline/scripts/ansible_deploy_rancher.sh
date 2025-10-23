#!/bin/bash
set -e

# Ansible Rancher Deployment Script
# Consolidated script that handles Rancher Helm deployment and validation
# Replaces: ansible_run_rancher_deployment.sh

# Standard script metadata
readonly SCRIPT_NAME="$(basename "$0")"
readonly SCRIPT_DIR="$(dirname "$0")"
readonly QA_INFRA_CLONE_PATH="/root/qa-infra-automation"
readonly RANCHER_PLAYBOOK="$QA_INFRA_CLONE_PATH/ansible/rke2/airgap/playbooks/deploy/rancher-helm-deploy-playbook.yml"
 
# Load the airgap library (try multiple candidate locations)
# shellcheck disable=SC1090
if ! type log_info >/dev/null 2>&1; then
  lib_candidates=(
    "${SCRIPT_DIR}/airgap_lib.sh"
    "/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap_lib.sh"
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
# RANCHER DEPLOYMENT
# =============================================================================

deploy_rancher() {
    local workspace="${1:-$TF_WORKSPACE}"

    log_info "Starting Rancher deployment for workspace: $workspace"

    # Validate prerequisites
    validate_rancher_prerequisites



    # Prepare Rancher deployment variables
    prepare_rancher_variables

    # Run Rancher deployment playbook
    run_rancher_playbook

    # Note: Verification is handled by the Ansible playbook
    log_info "Rancher deployment verification is handled by the Ansible playbook"

    # Generate deployment report
    generate_rancher_deployment_report

    log_success "Rancher deployment completed successfully"
}

# =============================================================================
# PREREQUISITE VALIDATION
# =============================================================================

validate_rancher_prerequisites() {
    log_info "Validating Rancher deployment prerequisites"

    local validation_errors=0

    # Check if RKE2 cluster is accessible
    # local kubeconfig_locations=(
    #     "$SHARED_VOLUME_PATH/kubeconfig.yaml"
    #     "/root/.kube/config"
    #     "/etc/rancher/rke2/rke2.yaml"
    # )

    # local kubeconfig_found=false
    # for config_path in "${kubeconfig_locations[@]}"; do
    #     if [[ -f "$config_path" ]]; then
    #         export KUBECONFIG="$config_path"
    #         if kubectl cluster-info &>/dev/null; then
    #             log_info "  ✓ Working kubeconfig found: $config_path"
    #             kubeconfig_found=true
    #             break
    #         else
    #             log_warning "  ⚠ Kubeconfig found but cluster not accessible: $config_path"
    #         fi
    #     fi
    # done

    # if [[ "$kubeconfig_found" == "false" ]]; then
    #     log_error "  ✗ No working kubeconfig found"
    #     ((validation_errors++))
    # fi

    # Check qa-infra-automation repository
    if [[ -d "$QA_INFRA_CLONE_PATH" ]]; then
        log_info "  ✓ qa-infra-automation repository exists"
    else
        log_error "  ✗ qa-infra-automation repository not found"
        ((validation_errors++))
    fi

    # Check Rancher playbook
    if [[ -f "$RANCHER_PLAYBOOK" ]]; then
        log_info "  ✓ Rancher deployment playbook exists: $RANCHER_PLAYBOOK"
    else
        log_error "  ✗ Rancher deployment playbook not found: $RANCHER_PLAYBOOK"
        ((validation_errors++))
    fi

    # Check required files
    local required_files=(
        "/root/ansible/rke2/airgap/inventory.yml"
        "/root/ansible/rke2/airgap/group_vars/all.yml"
    )

    log_info "Checking required files:"
    for file in "${required_files[@]}"; do
        if [[ -f "$file" && -s "$file" ]]; then
            log_info "  ✓ $file exists and is not empty"
        else
            log_error "  ✗ $file is missing or empty"
            ((validation_errors++))
        fi
    done

    # Check environment variables
    local important_vars=("RANCHER_VERSION" "HOSTNAME_PREFIX")
    log_info "Checking important environment variables:"
    for var in "${important_vars[@]}"; do
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
        log_error "Rancher prerequisite validation failed with $validation_errors errors"
        exit 1
    fi

    log_success "Rancher prerequisite validation passed"
}



# =============================================================================
# RANCHER VARIABLES PREPARATION
# =============================================================================

prepare_rancher_variables() {
    log_info "Preparing Rancher deployment variables"

    local group_vars_file="/root/ansible/rke2/airgap/group_vars/all.yml"

    # Ensure required Rancher variables are set
    local rancher_vars=(
        "rancher_version:${RANCHER_VERSION:-latest}"
        "hostname_prefix:${HOSTNAME_PREFIX:-rancher}"
    )

    for var_def in "${rancher_vars[@]}"; do
        local var_name="${var_def%:*}"
        local var_value="${var_def#*:}"

        # Check if variable exists in group_vars
        if grep -q "^${var_name}:" "$group_vars_file"; then
            log_info "Updating $var_name in group_vars: $var_value"
            sed -i "s/^${var_name}:.*/${var_name}: \"${var_value}\"/" "$group_vars_file"
        else
            log_info "Adding $var_name to group_vars: $var_value"
            echo "${var_name}: \"${var_value}\"" >> "$group_vars_file"
        fi
    done

    # Ensure Rancher hostname is properly configured
    if [[ -n "${HOSTNAME_PREFIX}" ]]; then
        local rancher_hostname="${HOSTNAME_PREFIX}.qa.rancher.space"
        if grep -q "^rancher_hostname:" "$group_vars_file"; then
            log_info "Updating rancher_hostname: $rancher_hostname"
            sed -i "s/^rancher_hostname:.*/rancher_hostname: \"${rancher_hostname}\"/" "$group_vars_file"
        else
            log_info "Adding rancher_hostname: $rancher_hostname"
            echo "rancher_hostname: \"${rancher_hostname}\"" >> "$group_vars_file"
        fi
    fi

    # Show key Rancher variables
    log_info "=== Key Rancher Variables ==="
    local key_vars=("rancher_version" "rancher_hostname" "hostname_prefix")
    for var in "${key_vars[@]}"; do
        if grep -q "^$var:" "$group_vars_file"; then
            local value
            value=$(grep "^$var:" "$group_vars_file" | cut -d'"' -f2)
            log_info "  $var: $value"
        fi
    done
    log_info "=== End Key Variables ==="

    # Validate the updated group_vars file
    validate_yaml_syntax "$group_vars_file"

    log_success "Rancher variables preparation completed"
}

# =============================================================================
# RANCHER PLAYBOOK EXECUTION
# =============================================================================

run_rancher_playbook() {
    log_info "Running Rancher deployment playbook"

    local inventory_file="/root/ansible/rke2/airgap/inventory.yml"
    local group_vars_file="/root/ansible/rke2/airgap/group_vars/all.yml"
    local log_file="$SHARED_VOLUME_PATH/rancher_deployment.log"

    # Check for test mode
    if grep -q "test_force_failure: true" "$group_vars_file" 2>/dev/null; then
        log_warning "=== TEST MODE DETECTED ==="
        log_warning "test_force_failure: true found in group_vars"
        log_warning "This will force Rancher deployment failure"
        log_warning "To disable, remove 'test_force_failure: true' from group_vars"
        log_warning "=== END TEST MODE ==="
        sleep 5
        exit 1
    fi

    # Prepare extra variables safely as an array to avoid word-splitting/globbing
    local -a extra_args=()
    [[ -n "${RANCHER_VERSION:-}" ]] && { extra_args+=( -e "rancher_version=${RANCHER_VERSION}" ); log_info "Passing rancher_version as extra variable: ${RANCHER_VERSION}"; }
    [[ -n "${HOSTNAME_PREFIX:-}" ]] && { extra_args+=( -e "hostname_prefix=${HOSTNAME_PREFIX}" ); log_info "Passing hostname_prefix as extra variable: ${HOSTNAME_PREFIX}"; }

    # Change to playbook directory
    cd "$QA_INFRA_CLONE_PATH/ansible/rke2/airgap" || {
        log_error "Failed to change to playbook directory"
        exit 1
    }

    log_info "Executing: ansible-playbook -i $inventory_file $RANCHER_PLAYBOOK -v ${extra_args[*]}"

    # Run the playbook with logging
    if ansible-playbook -i "$inventory_file" "$RANCHER_PLAYBOOK" -v "${extra_args[@]}" 2>&1 | tee "$log_file"; then
        log_success "Rancher deployment playbook completed successfully"
        export RANCHER_ANSIBLE_EXIT_CODE=0
    else
        local exit_code=$?
        log_error "Rancher deployment playbook failed (exit code: $exit_code)"
        export RANCHER_ANSIBLE_EXIT_CODE=$exit_code
        return 1
    fi

    # Copy execution log to shared volume
    if [[ -f "ansible-playbook.log" ]]; then
        cp ansible-playbook.log "$SHARED_VOLUME_PATH/rancher_playbook_execution.log"
    fi
}



# =============================================================================
# RANCHER DEPLOYMENT REPORT
# =============================================================================

generate_rancher_deployment_report() {
    log_info "Generating Rancher deployment report"

    local report_file="$SHARED_VOLUME_PATH/rancher_deployment_report.txt"

    cat > "$report_file" << EOF
# Rancher Deployment Report
# Generated on: $(date)
# Workspace: $TF_WORKSPACE
# Script: $SCRIPT_NAME

Deployment Summary:
- Start time: $(date)
- Rancher Version: ${RANCHER_VERSION:-not specified}
- Hostname Prefix: ${HOSTNAME_PREFIX:-not specified}
- Ansible Exit Code: ${RANCHER_ANSIBLE_EXIT_CODE:-not set}
- Working Directory: $(pwd)

Rancher Status:
EOF

    # Note: Rancher pod and service information is handled by Ansible playbook
    echo "- Rancher Status: Verification handled by Ansible playbook" >> "$report_file"

    # Add Rancher URL information
    local group_vars_file="/root/ansible/rke2/airgap/group_vars/all.yml"
    if grep -q "^rancher_hostname:" "$group_vars_file"; then
        local rancher_url
        rancher_url=$(grep "^rancher_hostname:" "$group_vars_file" | cut -d'"' -f2)
        echo "" >> "$report_file"
        echo "- Rancher URL: https://$rancher_url" >> "$report_file"
    fi

    cat >> "$report_file" << EOF

Artifacts Generated:
- $SHARED_VOLUME_PATH/rancher_deployment.log
- $SHARED_VOLUME_PATH/rancher_playbook_execution.log
- $SHARED_VOLUME_PATH/rancher_deployment_report.txt

EOF

    # Add recommendations
    if [[ "${RANCHER_ANSIBLE_EXIT_CODE}" -eq 0 ]]; then
        cat >> "$report_file" << EOF
Recommendations:
- Rancher deployment completed successfully
- Access Rancher UI using the configured hostname
- Proceed with initial Rancher setup and configuration
- Consider enabling backup and monitoring

Next Steps:
1. Access Rancher UI at https://$rancher_url
2. Complete initial Rancher setup
3. Configure authentication providers
4. Create or import clusters
EOF
    else
        cat >> "$report_file" << EOF
Recommendations:
- Rancher deployment had issues
- Check deployment logs for details
- Verify cluster connectivity and resources
- Consider manual intervention or redeployment

Troubleshooting:
1. Check Rancher pod logs: kubectl logs -n cattle-system deployment/rancher
2. Verify cluster resources: kubectl get nodes, kubectl get pods -A
3. Check network connectivity and DNS resolution
EOF
    fi

    log_success "Rancher deployment report generated: $report_file"
}

# =============================================================================
# HELP AND USAGE
# =============================================================================

show_help() {
    cat << EOF
Usage: $SCRIPT_NAME [OPTIONS]

Ansible Rancher Deployment Script
This script deploys Rancher using Helm charts via qa-infra-automation playbooks.

OPTIONS:
    -w, --workspace WORKSPACE    Terraform workspace name (default: \$TF_WORKSPACE)
    -h, --help                 Show this help message
    --debug                   Enable debug logging


ENVIRONMENT VARIABLES:
    TF_WORKSPACE              Terraform workspace name
    RANCHER_VERSION           Rancher version to deploy
    HOSTNAME_PREFIX           Prefix for Rancher hostname
    ANSIBLE_VARIABLES         Ansible variables content
    DEBUG                     Enable debug logging (true/false)

EXAMPLES:
    # Deploy Rancher with default settings
    $SCRIPT_NAME

    # Deploy with specific Rancher version and hostname
    RANCHER_VERSION=v2.8.0 HOSTNAME_PREFIX=my-rancher $SCRIPT_NAME

    # Deploy with debug logging
    DEBUG=true $SCRIPT_NAME --debug



EOF
}

# =============================================================================
# ARGUMENT PARSING
# =============================================================================

parse_arguments() {
    local workspace="$TF_WORKSPACE"

    while [[ $# -gt 0 ]]; do
        case $1 in
            -w|--workspace)
                workspace="$2"
                shift 2
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

    log_info "Configuration:"
    log_info "  Workspace: $workspace"
    log_info "  Debug mode: ${DEBUG:-false}"
}

# =============================================================================
# MAIN SCRIPT EXECUTION
# =============================================================================

main() {
    log_info "=== Rancher Deployment Started ==="
    log_info "Script: $SCRIPT_NAME"
    log_info "Timestamp: $(date)"
    log_info "Working directory: $(pwd)"

    # Parse command line arguments
    parse_arguments "$@"

    # Initialize the airgap environment
    initialize_airgap_environment

    # Wait for confirmation if in interactive mode
    wait_for_confirmation "Press Enter to start Rancher deployment..."

    # Run the deployment
    deploy_rancher "$TF_WORKSPACE"

    log_success "=== Rancher Deployment Completed ==="
}

# Error handling
trap 'log_error "Script failed at line $LINENO"' ERR

# Execute main function with all arguments
main "$@"
