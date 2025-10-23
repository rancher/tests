#!/bin/bash
set -e

# Script to handle Rancher deployment failure cleanup
# This script is executed within the Docker container when Rancher deployment fails

# =============================================================================
# CONSTANTS
# =============================================================================

readonly SCRIPT_NAME="$(basename "$0")"
readonly SCRIPT_DIR="$(dirname "$0")"
readonly FAILURE_REPORT_DIR="/root/rancher-failure-report"

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
  fi

  [[ -n "${QA_INFRA_WORK_PATH:-}" ]] || { log_error "QA_INFRA_WORK_PATH not set"; exit 1; }
  [[ -d "${QA_INFRA_WORK_PATH}" ]] || { log_error "QA_INFRA_WORK_PATH directory not found"; exit 1; }
}

# =============================================================================
# MAIN FUNCTION
# =============================================================================

main() {
  log_info "Starting Rancher deployment failure cleanup with $SCRIPT_NAME"
  log_info "RKE2 Version: ${RKE2_VERSION:-'NOT SET'}"
  log_info "Rancher Version: ${RANCHER_VERSION:-'NOT SET'}"
  log_info "DESTROY_ON_FAILURE: ${DESTROY_ON_FAILURE:-'false'}"

  # Validate prerequisites
  validate_prerequisites

  # Change to the qa-infra-automation directory
  cd "${QA_INFRA_WORK_PATH}" || {
    log_error "Failed to change to QA_INFRA_WORK_PATH: ${QA_INFRA_WORK_PATH}"
    exit 1
  }

  # Change to the airgap ansible directory
  cd ansible/rke2/airgap || {
    log_error "Failed to change to ansible directory"
    exit 1
  }

echo "=== Collecting Rancher Deployment Failure Information ==="

# Create failure report directory
FAILURE_REPORT_DIR="/root/rancher-failure-report"
mkdir -p "${FAILURE_REPORT_DIR}"

# Collect system information
echo "Collecting system information..."
uname -a > "${FAILURE_REPORT_DIR}/system-info.txt"
df -h >> "${FAILURE_REPORT_DIR}/system-info.txt"
free -h >> "${FAILURE_REPORT_DIR}/system-info.txt"

# Collect Ansible configuration
echo "Collecting Ansible configuration..."
if [[ -f "inventory.yml" ]]; then
    cp inventory.yml "${FAILURE_REPORT_DIR}/"
fi

if [[ -f "group_vars/all.yml" ]]; then
    cp group_vars/all.yml "${FAILURE_REPORT_DIR}/"
fi

# Collect kubectl information if available
echo "Collecting Kubernetes cluster information..."
if command -v kubectl &> /dev/null; then
    # Try to get cluster information
    kubectl version --client > "${FAILURE_REPORT_DIR}/kubectl-version.txt" 2>&1 || echo "kubectl version failed" > "${FAILURE_REPORT_DIR}/kubectl-version.txt"
    
    # Try to get cluster nodes
    kubectl get nodes -o wide > "${FAILURE_REPORT_DIR}/cluster-nodes.txt" 2>&1 || echo "kubectl get nodes failed" > "${FAILURE_REPORT_DIR}/cluster-nodes.txt"
    
    # Try to get all namespaces
    kubectl get namespaces > "${FAILURE_REPORT_DIR}/cluster-namespaces.txt" 2>&1 || echo "kubectl get namespaces failed" > "${FAILURE_REPORT_DIR}/cluster-namespaces.txt"
    
    # Try to get Rancher-related resources
    kubectl get all -n cattle-system > "${FAILURE_REPORT_DIR}/rancher-resources.txt" 2>&1 || echo "kubectl get rancher resources failed" > "${FAILURE_REPORT_DIR}/rancher-resources.txt"
    
    # Get pod logs for any Rancher pods
    echo "Collecting Rancher pod logs..."
    RANCHER_PODS=$(kubectl get pods -n cattle-system -l app=rancher --no-headers 2>/dev/null | awk '{print $1}' || echo "")
    if [[ -n "${RANCHER_PODS}" ]]; then
        for pod in ${RANCHER_PODS}; do
            echo "Collecting logs for pod: ${pod}"
            kubectl logs "${pod}" -n cattle-system > "${FAILURE_REPORT_DIR}/rancher-pod-${pod}.log" 2>&1 || echo "Failed to get logs for ${pod}"
        done
    fi
else
    echo "kubectl command not available" > "${FAILURE_REPORT_DIR}/kubectl-not-available.txt"
fi

# Collect system logs
echo "Collecting system logs..."
if [[ -d "/var/log" ]]; then
    # Collect relevant system logs
    for log_file in syslog messages kern.log; do
        if [[ -f "/var/log/${log_file}" ]]; then
            tail -100 "/var/log/${log_file}" > "${FAILURE_REPORT_DIR}/system-${log_file}.tail" 2>&1 || echo "Failed to collect ${log_file}"
        fi
    done
fi

# Collect Docker/container logs if available
echo "Collecting Docker/container logs..."
if command -v docker &> /dev/null; then
    docker ps -a > "${FAILURE_REPORT_DIR}/docker-containers.txt" 2>&1 || echo "Failed to get docker containers"
    
    # Get logs for Rancher-related containers
    RANCHER_CONTAINERS=$(docker ps -a --filter "name=rancher" --format "{{.Names}}" 2>/dev/null || echo "")
    if [[ -n "${RANCHER_CONTAINERS}" ]]; then
        for container in ${RANCHER_CONTAINERS}; do
            echo "Collecting logs for container: ${container}"
            docker logs --tail 100 "${container}" > "${FAILURE_REPORT_DIR}/docker-container-${container}.log" 2>&1 || echo "Failed to get logs for ${container}"
        done
    fi
fi

# Collect network information
echo "Collecting network information..."
if command -v netstat &> /dev/null; then
    netstat -tlnp > "${FAILURE_REPORT_DIR}/network-connections.txt" 2>&1 || echo "Failed to get network connections"
fi

if command -v ss &> /dev/null; then
    ss -tlnp > "${FAILURE_REPORT_DIR}/network-connections-ss.txt" 2>&1 || echo "Failed to get network connections with ss"
fi

# Create failure summary report
echo "Creating failure summary report..."
cat > "${FAILURE_REPORT_DIR}/failure-summary.txt" << EOF
Rancher Deployment Failure Summary
=================================

Timestamp: $(date -u +%Y-%m-%dT%H:%M:%SZ)
RKE2 Version: ${RKE2_VERSION:-'NOT SET'}
Rancher Version: ${RANCHER_VERSION:-'NOT SET'}
DESTROY_ON_FAILURE: ${DESTROY_ON_FAILURE:-'false'}

Environment Variables:
----------------------
$(env | grep -E '^(RKE2_|RANCHER_|QA_INFRA_|AWS_|TF_)' | sort)

Ansible Configuration:
---------------------
$(if [[ -f "group_vars/all.yml" ]]; then echo "group_vars/all.yml exists"; else echo "group_vars/all.yml NOT FOUND"; fi)
$(if [[ -f "inventory.yml" ]]; then echo "inventory.yml exists"; else echo "inventory.yml NOT FOUND"; fi)

Cluster Status:
--------------
$(if command -v kubectl &> /dev/null && kubectl cluster-info &> /dev/null; then
    echo "Cluster is accessible"
    echo "Nodes: $(kubectl get nodes --no-headers 2>/dev/null | wc -l || echo "Unknown")"
    echo "Namespaces: $(kubectl get namespaces --no-headers 2>/dev/null | wc -l || echo "Unknown")"
else
    echo "Cluster is NOT accessible or kubectl not available"
fi)

Rancher Resources:
-----------------
$(if command -v kubectl &> /dev/null; then
    if kubectl get namespace cattle-system &> /dev/null; then
        echo "cattle-system namespace exists"
        echo "Rancher pods: $(kubectl get pods -n cattle-system -l app=rancher --no-headers 2>/dev/null | wc -l || echo "Unknown")"
        echo "Rancher services: $(kubectl get services -n cattle-system --no-headers 2>/dev/null | wc -l || echo "Unknown")"
    else
        echo "cattle-system namespace NOT FOUND"
    fi
else
    echo "kubectl not available"
fi)

Recent System Activity:
----------------------
$(if [[ -f "/var/log/syslog" ]]; then tail -20 /var/log/syslog; else echo "System logs not available"; fi)

EOF

# Archive the failure report
echo "Archiving failure report..."
cd /root
tar -czf "rancher-failure-report-$(date +%Y%m%d-%H%M%S).tar.gz" rancher-failure-report/

# Check if we should destroy infrastructure on failure
if [[ "${DESTROY_ON_FAILURE}" == "true" ]]; then
    echo "=== DESTROY_ON_FAILURE is true - Attempting Infrastructure Cleanup ==="
    
    # Try to run any available cleanup playbooks
    echo "Checking for Rancher cleanup playbooks..."
    
    # Look for Rancher uninstall/cleanup playbooks
    RANCHER_CLEANUP_PLAYBOOKS=(
        "playbooks/deploy/rancher-helm-uninstall.yml"
        "playbooks/cleanup/rancher-cleanup.yml"
        "playbooks/deploy/rancher-cleanup.yml"
    )
    
    for playbook in "${RANCHER_CLEANUP_PLAYBOOKS[@]}"; do
        if [[ -f "${playbook}" ]]; then
            echo "Found Rancher cleanup playbook: ${playbook}"
            echo "Running Rancher cleanup playbook..."
            
            # Run the cleanup playbook
            if ansible-playbook -i inventory.yml "${playbook}" -v > "${FAILURE_REPORT_DIR}/rancher-cleanup-playbook.log" 2>&1; then
                echo "[OK] Rancher cleanup playbook executed successfully"
            else
                echo "[FAIL] Rancher cleanup playbook failed"
                echo "Check ${FAILURE_REPORT_DIR}/rancher-cleanup-playbook.log for details"
            fi
            break
        fi
    done
    
    # If no specific Rancher cleanup playbook found, try manual cleanup
    if [[ ! -f "${RANCHER_CLEANUP_PLAYBOOKS[0]}" && ! -f "${RANCHER_CLEANUP_PLAYBOOKS[1]}" && ! -f "${RANCHER_CLEANUP_PLAYBOOKS[2]}" ]]; then
        echo "No specific Rancher cleanup playbook found, attempting manual cleanup..."
        
        if command -v kubectl &> /dev/null && kubectl cluster-info &> /dev/null; then
            echo "Attempting to remove Rancher resources manually..."
            
            # Delete Rancher namespace and resources
            kubectl delete namespace cattle-system --ignore-not-found=true > "${FAILURE_REPORT_DIR}/manual-cleanup.log" 2>&1 || echo "Failed to delete cattle-system namespace" >> "${FAILURE_REPORT_DIR}/manual-cleanup.log"
            
            # Remove Rancher CRDs
            kubectl delete crds --selector=app.kubernetes.io/name=rancher --ignore-not-found=true >> "${FAILURE_REPORT_DIR}/manual-cleanup.log" 2>&1 || echo "Failed to delete Rancher CRDs" >> "${FAILURE_REPORT_DIR}/manual-cleanup.log"
            
            echo "Manual cleanup completed"
        else
            echo "Cannot perform manual cleanup - cluster not accessible"
        fi
    fi
    
    echo "Infrastructure cleanup attempt completed"
else
    echo "=== DESTROY_ON_FAILURE is false - Skipping Infrastructure Cleanup ==="
    echo "Manual cleanup will be required"
    echo "Infrastructure resources remain intact for debugging"
fi

# Copy failure report to shared volume for Jenkins to archive
echo "Copying failure report to shared volume..."
cp /root/rancher-failure-report-*.tar.gz /root/ 2>/dev/null || echo "Failed to copy failure report to shared volume"

  log_info "Rancher deployment failure cleanup completed"
  log_info "Failure report has been generated and archived"
  log_info "Check the archived artifacts for detailed failure information"
}

# Error handling
trap 'log_error "Script failed at line $LINENO"' ERR

# Execute main function
main "$@"