#!/bin/bash
set -e

# Ansible Rancher Deployment Script
# Consolidated script that handles Rancher Helm deployment and validation
# Replaces: ansible_run_rancher_deployment.sh

# Load the airgap library
source "/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap_lib.sh"

# =============================================================================
# SCRIPT CONFIGURATION
# =============================================================================

readonly SCRIPT_NAME="$(basename "$0")"
readonly SCRIPT_DIR="$(dirname "$0")"
readonly QA_INFRA_CLONE_PATH="/root/qa-infra-automation"
readonly RANCHER_PLAYBOOK="$QA_INFRA_CLONE_PATH/ansible/rke2/airgap/playbooks/deploy/rancher-helm-playbook.yml"

# =============================================================================
# RANCHER DEPLOYMENT
# =============================================================================

deploy_rancher() {
    local workspace="${1:-$TF_WORKSPACE}"

    log_info "Starting Rancher deployment for workspace: $workspace"

    # Validate prerequisites
    validate_rancher_prerequisites

    # Validate cluster connectivity
    validate_cluster_connectivity

    # Prepare Rancher deployment variables
    prepare_rancher_variables

    # Run Rancher deployment playbook
    run_rancher_playbook

    # Verify Rancher deployment
    verify_rancher_deployment

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
                log_info "  ✓ Working kubeconfig found: $config_path"
                kubeconfig_found=true
                break
            else
                log_warning "  ⚠ Kubeconfig found but cluster not accessible: $config_path"
            fi
        fi
    done

    if [[ "$kubeconfig_found" == "false" ]]; then
        log_error "  ✗ No working kubeconfig found"
        ((validation_errors++))
    fi

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
# CLUSTER CONNECTIVITY VALIDATION
# =============================================================================

validate_cluster_connectivity() {
    log_info "Validating cluster connectivity"

    # Check node status
    log_info "Checking cluster node status..."
    if kubectl get nodes --no-headers 2>/dev/null | grep -q "Ready"; then
        local ready_nodes
        ready_nodes=$(kubectl get nodes --no-headers | grep "Ready" | wc -l)
        local total_nodes
        total_nodes=$(kubectl get nodes --no-headers | wc -l)
        log_info "Cluster nodes: $ready_nodes/$total_nodes ready"

        if [[ $ready_nodes -eq 0 ]]; then
            log_error "No ready nodes found in cluster"
            exit 1
        fi
    else
        log_error "Cannot access cluster nodes"
        exit 1
    fi

    # Check system pods
    log_info "Checking system pods..."
    if kubectl get pods -n kube-system --no-headers 2>/dev/null | grep -q "Running"; then
        local running_pods
        running_pods=$(kubectl get pods -n kube-system --no-headers | grep "Running" | wc -l)
        local total_pods
        total_pods=$(kubectl get pods -n kube-system --no-headers | wc -l)
        log_info "System pods: $running_pods/$total_pods running"

        if [[ $running_pods -eq 0 ]]; then
            log_warning "No running system pods found - cluster may not be fully ready"
        fi
    else
        log_warning "Cannot access system pods"
    fi

    # Check cluster info
    log_info "Cluster access test:"
    if kubectl cluster-info &>/dev/null; then
        log_success "Cluster is accessible"
    else
        log_error "Cannot access cluster info"
        exit 1
    fi

    log_success "Cluster connectivity validation passed"
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

    # Prepare extra variables
    local extra_vars=""
    if [[ -n "${RANCHER_VERSION}" ]]; then
        extra_vars="-e rancher_version=${RANCHER_VERSION}"
        log_info "Passing rancher_version as extra variable: ${RANCHER_VERSION}"
    fi

    if [[ -n "${HOSTNAME_PREFIX}" ]]; then
        extra_vars="$extra_vars -e hostname_prefix=${HOSTNAME_PREFIX}"
        log_info "Passing hostname_prefix as extra variable: ${HOSTNAME_PREFIX}"
    fi

    # Change to playbook directory
    cd "$QA_INFRA_CLONE_PATH/ansible/rke2/airgap" || {
        log_error "Failed to change to playbook directory"
        exit 1
    }

    log_info "Executing: ansible-playbook -i $inventory_file $RANCHER_PLAYBOOK -v $extra_vars"

    # Run the playbook with logging
    if ansible-playbook -i "$inventory_file" "$RANCHER_PLAYBOOK" -v $extra_vars 2>&1 | tee "$log_file"; then
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
# RANCHER DEPLOYMENT VERIFICATION
# =============================================================================

verify_rancher_deployment() {
    log_info "Verifying Rancher deployment"

    if [[ "${RANCHER_ANSIBLE_EXIT_CODE}" -ne 0 ]]; then
        log_error "Rancher deployment failed, skipping verification"
        return 1
    fi

    # Wait for Rancher pods to be ready
    wait_for_rancher_pods

    # Check Rancher system components
    verify_rancher_components

    # Verify Rancher accessibility
    verify_rancher_accessibility

    log_success "Rancher deployment verification completed"
}

# =============================================================================
# RANCHER PODS VERIFICATION
# =============================================================================

wait_for_rancher_pods() {
    log_info "Waiting for Rancher pods to be ready..."

    local max_attempts=30
    local attempt=1

    while [[ $attempt -le $max_attempts ]]; do
        log_info "Checking Rancher pods (attempt $attempt/$max_attempts)..."

        # Check for Rancher pods
        if kubectl get pods -n cattle-system --no-headers 2>/dev/null | grep -q "Running"; then
            local running_pods
            running_pods=$(kubectl get pods -n cattle-system --no-headers | grep "Running" | wc -l)
            local total_pods
            total_pods=$(kubectl get pods -n cattle-system --no-headers | wc -l)
            log_info "Rancher pods: $running_pods/$total_pods running"

            if [[ $running_pods -eq $total_pods && $total_pods -gt 0 ]]; then
                log_success "All Rancher pods are running"
                return 0
            fi
        fi

        # Check for cattle-fleet pods if fleet is installed
        if kubectl get namespaces | grep -q "cattle-fleet-system"; then
            if kubectl get pods -n cattle-fleet-system --no-headers 2>/dev/null | grep -q "Running"; then
                local fleet_running
                fleet_running=$(kubectl get pods -n cattle-fleet-system --no-headers | grep "Running" | wc -l)
                local fleet_total
                fleet_total=$(kubectl get pods -n cattle-fleet-system --no-headers | wc -l)
                log_info "Fleet pods: $fleet_running/$fleet_total running"
            fi
        fi

        if [[ $attempt -eq $max_attempts ]]; then
            log_warning "Timeout waiting for Rancher pods to be ready"
            kubectl get pods -A | grep -E "(cattle|rancher)" || true
        fi

        sleep 10
        ((attempt++))
    done
}

# =============================================================================
# RANCHER COMPONENTS VERIFICATION
# =============================================================================

verify_rancher_components() {
    log_info "Verifying Rancher system components"

    # Check Rancher namespace
    if kubectl get namespace cattle-system &>/dev/null; then
        log_success "Rancher namespace exists: cattle-system"
    else
        log_error "Rancher namespace not found"
        return 1
    fi

    # Check Rancher deployment
    if kubectl get deployment rancher -n cattle-system &>/dev/null; then
        log_success "Rancher deployment found"
        local replicas
        replicas=$(kubectl get deployment rancher -n cattle-system -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")
        log_info "Rancher ready replicas: $replicas"
    else
        log_error "Rancher deployment not found"
        return 1
    fi

    # Check Rancher service
    if kubectl get service rancher -n cattle-system &>/dev/null; then
        log_success "Rancher service found"
        local service_type
        service_type=$(kubectl get service rancher -n cattle-system -o jsonpath='{.spec.type}' 2>/dev/null || echo "unknown")
        log_info "Rancher service type: $service_type"
    else
        log_error "Rancher service not found"
        return 1
    fi

    # Check Ingress if available
    if command -v nginx &>/dev/null || kubectl get ingress -n cattle-system &>/dev/null; then
        if kubectl get ingress -n cattle-system --no-headers 2>/dev/null | grep -q "rancher"; then
            log_success "Rancher ingress found"
        else
            log_info "Rancher ingress not found (may not be required)"
        fi
    fi

    log_success "Rancher components verification completed"
}

# =============================================================================
# RANCHER ACCESSIBILITY VERIFICATION
# =============================================================================

verify_rancher_accessibility() {
    log_info "Verifying Rancher accessibility"

    # Get Rancher URL from group_vars or service
    local rancher_url=""
    local group_vars_file="/root/ansible/rke2/airgap/group_vars/all.yml"

    if grep -q "^rancher_hostname:" "$group_vars_file"; then
        rancher_url=$(grep "^rancher_hostname:" "$group_vars_file" | cut -d'"' -f2)
        log_info "Rancher URL from group_vars: $rancher_url"
    fi

    # Try to get Rancher URL from service (if LoadBalancer)
    if [[ -z "$rancher_url" ]]; then
        local service_type
        service_type=$(kubectl get service rancher -n cattle-system -o jsonpath='{.spec.type}' 2>/dev/null || echo "")

        if [[ "$service_type" == "LoadBalancer" ]]; then
            local lb_ip
            lb_ip=$(kubectl get service rancher -n cattle-system -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || echo "")
            if [[ -n "$lb_ip" ]]; then
                rancher_url="https://$lb_ip"
                log_info "Rancher URL from LoadBalancer: $rancher_url"
            fi
        fi
    fi

    # Try to check Rancher API health
    if [[ -n "$rancher_url" ]]; then
        log_info "Testing Rancher API accessibility..."
        if command -v curl &>/dev/null; then
            # Use a simple health check (avoiding authentication requirements)
            if curl -k -s --connect-timeout 10 "$rancher_url/ping" 2>/dev/null | grep -q "pong"; then
                log_success "Rancher API is accessible: $rancher_url"
            else
                log_info "Rancher API health check failed (may require authentication)"
                log_info "This is normal for Rancher without proper credentials"
            fi
        else
            log_info "curl not available, skipping API accessibility test"
        fi
    fi

    # Check Rancher version via API if possible
    if kubectl get pods -n cattle-system --no-headers 2>/dev/null | grep -q "rancher"; then
        local rancher_pod
        rancher_pod=$(kubectl get pods -n cattle-system -l app=rancher -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
        if [[ -n "$rancher_pod" ]]; then
            log_info "Found Rancher pod: $rancher_pod"
            # Try to get Rancher version from pod
            local rancher_image
            rancher_image=$(kubectl get pod "$rancher_pod" -n cattle-system -o jsonpath='{.spec.containers[0].image}' 2>/dev/null || echo "")
            if [[ -n "$rancher_image" ]]; then
                log_info "Rancher image: $rancher_image"
            fi
        fi
    fi

    log_success "Rancher accessibility verification completed"
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

    # Add Rancher pod information
    if kubectl get pods -n cattle-system &>/dev/null; then
        echo "- Rancher Pods:" >> "$report_file"
        kubectl get pods -n cattle-system >> "$report_file" 2>&1
    else
        echo "- Rancher Pods: Not accessible" >> "$report_file"
    fi

    # Add Rancher service information
    if kubectl get service rancher -n cattle-system &>/dev/null; then
        echo "" >> "$report_file"
        echo "- Rancher Service:" >> "$report_file"
        kubectl get service rancher -n cattle-system >> "$report_file" 2>&1
    fi

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
    --skip-verification       Skip post-deployment verification

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

    # Deploy without verification
    $SCRIPT_NAME --skip-verification

EOF
}

# =============================================================================
# ARGUMENT PARSING
# =============================================================================

parse_arguments() {
    local workspace="$TF_WORKSPACE"
    local skip_verification="false"

    while [[ $# -gt 0 ]]; do
        case $1 in
            -w|--workspace)
                workspace="$2"
                shift 2
                ;;
            --skip-verification)
                skip_verification="true"
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
    export SKIP_VERIFICATION="$skip_verification"

    log_info "Configuration:"
    log_info "  Workspace: $workspace"
    log_info "  Skip verification: $skip_verification"
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