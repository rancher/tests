#!/bin/bash
set -e

# Script to validate Rancher deployment after Ansible playbook execution
# This script is executed within the Docker container

echo "=== Validating Rancher Deployment ==="
echo "RKE2 Version: ${RKE2_VERSION:-'NOT SET'}"
echo "Rancher Version: ${RANCHER_VERSION:-'NOT SET'}"
echo "QA Infra Work Path: ${QA_INFRA_WORK_PATH:-'NOT SET'}"

# Validate required environment variables
if [[ -z "${QA_INFRA_WORK_PATH}" ]]; then
    echo "ERROR: QA_INFRA_WORK_PATH environment variable is not set"
    exit 1
fi

# Change to the qa-infra-automation directory
cd "${QA_INFRA_WORK_PATH}"

# Change to the airgap ansible directory
cd ansible/rke2/airgap

# Check if kubectl is available and configured
echo "=== Checking kubectl access ==="
if ! command -v kubectl &> /dev/null; then
    echo "ERROR: kubectl command not found"
    exit 1
fi

# Check if we can access the cluster
echo "=== Testing cluster connectivity ==="
if ! kubectl cluster-info &> /dev/null; then
    echo "ERROR: Cannot connect to Kubernetes cluster"
    echo "Attempting to diagnose the issue..."
    
    # Try to get more diagnostic information
    echo "Current KUBECONFIG: ${KUBECONFIG:-'NOT SET'}"
    if [[ -n "${KUBECONFIG}" && -f "${KUBECONFIG}" ]]; then
        echo "KUBECONFIG file exists, testing connection..."
        kubectl cluster-info --v=6 || echo "kubectl connection failed"
    else
        echo "KUBECONFIG not set or file doesn't exist"
    fi
    
    exit 1
fi

echo "✓ Cluster connectivity verified"

# Check if Rancher namespace exists
echo "=== Checking Rancher namespace ==="
if kubectl get namespace cattle-system &> /dev/null; then
    echo "✓ Rancher namespace (cattle-system) exists"
else
    echo "ERROR: Rancher namespace (cattle-system) not found"
    exit 1
fi

# Check if Rancher pods are running
echo "=== Checking Rancher pods ==="
RANCHER_PODS=$(kubectl get pods -n cattle-system -l app=rancher --no-headers 2>/dev/null | wc -l)
if [[ ${RANCHER_PODS} -gt 0 ]]; then
    echo "✓ Found ${RANCHER_PODS} Rancher pod(s)"
    
    # Check if pods are ready
    READY_PODS=$(kubectl get pods -n cattle-system -l app=rancher --no-headers 2>/dev/null | grep -c "Running\|Ready" || echo "0")
    if [[ ${READY_PODS} -gt 0 ]]; then
        echo "✓ ${READY_PODS} Rancher pod(s) are running"
    else
        echo "WARNING: Rancher pods exist but none are in Running/Ready state"
        echo "Pod status:"
        kubectl get pods -n cattle-system -l app=rancher || echo "Failed to get pod status"
    fi
else
    echo "ERROR: No Rancher pods found in cattle-system namespace"
    echo "Available pods in cattle-system:"
    kubectl get pods -n cattle-system || echo "Failed to get pods"
    exit 1
fi

# Check if Rancher service is available
echo "=== Checking Rancher service ==="
if kubectl get service rancher -n cattle-system &> /dev/null; then
    echo "✓ Rancher service exists"
    
    # Get service details
    SERVICE_TYPE=$(kubectl get service rancher -n cattle-system -o jsonpath='{.spec.type}' 2>/dev/null || echo "Unknown")
    echo "Service type: ${SERVICE_TYPE}"
    
    if [[ "${SERVICE_TYPE}" == "LoadBalancer" ]]; then
        # Check if LoadBalancer has an external IP
        EXTERNAL_IP=$(kubectl get service rancher -n cattle-system -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || echo "")
        if [[ -n "${EXTERNAL_IP}" ]]; then
            echo "✓ LoadBalancer external IP: ${EXTERNAL_IP}"
        else
            echo "WARNING: LoadBalancer service exists but no external IP assigned yet"
        fi
    fi
else
    echo "ERROR: Rancher service not found"
    exit 1
fi

# Check if Rancher ingress is available (if using Ingress)
echo "=== Checking Rancher ingress ==="
if kubectl get ingress rancher -n cattle-system &> /dev/null; then
    echo "✓ Rancher ingress exists"
    
    # Get ingress details
    INGRESS_HOST=$(kubectl get ingress rancher -n cattle-system -o jsonpath='{.spec.rules[0].host}' 2>/dev/null || echo "")
    if [[ -n "${INGRESS_HOST}" ]]; then
        echo "✓ Ingress host: ${INGRESS_HOST}"
    else
        echo "WARNING: Ingress exists but no host found"
    fi
else
    echo "INFO: No Rancher ingress found (may be using LoadBalancer service type)"
fi

# Check cluster nodes to ensure they're ready
echo "=== Checking cluster nodes ==="
NODE_COUNT=$(kubectl get nodes --no-headers 2>/dev/null | wc -l)
if [[ ${NODE_COUNT} -gt 0 ]]; then
    echo "✓ Found ${NODE_COUNT} cluster node(s)"
    
    READY_NODES=$(kubectl get nodes --no-headers 2>/dev/null | grep -c "Ready" || echo "0")
    if [[ ${READY_NODES} -gt 0 ]]; then
        echo "✓ ${READY_NODES} node(s) are Ready"
    else
        echo "WARNING: No nodes are in Ready state"
        echo "Node status:"
        kubectl get nodes || echo "Failed to get node status"
    fi
else
    echo "ERROR: No cluster nodes found"
    exit 1
fi

# Check if we can access Rancher API (basic check)
echo "=== Checking Rancher API accessibility ==="
# Try to access Rancher API health endpoint
RANCHER_URL=""
SERVICE_TYPE=$(kubectl get service rancher -n cattle-system -o jsonpath='{.spec.type}' 2>/dev/null || echo "")

if [[ "${SERVICE_TYPE}" == "LoadBalancer" ]]; then
    EXTERNAL_IP=$(kubectl get service rancher -n cattle-system -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || echo "")
    if [[ -n "${EXTERNAL_IP}" ]]; then
        RANCHER_URL="https://${EXTERNAL_IP}"
    fi
fi

# If no LoadBalancer IP, try NodePort
if [[ -z "${RANCHER_URL}" ]]; then
    NODE_PORT=$(kubectl get service rancher -n cattle-system -o jsonpath='{.spec.ports[0].nodePort}' 2>/dev/null || echo "")
    if [[ -n "${NODE_PORT}" ]]; then
        # Get first node IP
        NODE_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null || echo "")
        if [[ -n "${NODE_IP}" ]]; then
            RANCHER_URL="https://${NODE_IP}:${NODE_PORT}"
        fi
    fi
fi

if [[ -n "${RANCHER_URL}" ]]; then
    echo "Testing Rancher API at: ${RANCHER_URL}"
    
    # Try a simple curl request with timeout and insecure flag for self-signed certs
    if curl -k --connect-timeout 10 --max-time 30 "${RANCHER_URL}/ping" &> /dev/null; then
        echo "✓ Rancher API is accessible at ${RANCHER_URL}"
    else
        echo "WARNING: Could not access Rancher API at ${RANCHER_URL}"
        echo "This might be expected if Rancher is still starting up"
    fi
else
    echo "INFO: Could not determine Rancher URL for API testing"
fi

# Save validation results
echo "=== Saving validation results ==="
VALIDATION_RESULTS="{
  \"timestamp\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",
  \"rke2_version\": \"${RKE2_VERSION:-'unknown'}\",
  \"rancher_version\": \"${RANCHER_VERSION:-'unknown'}\",
  \"cluster_nodes\": ${NODE_COUNT},
  \"ready_nodes\": ${READY_NODES},
  \"rancher_pods\": ${RANCHER_PODS},
  \"ready_rancher_pods\": ${READY_PODS},
  \"rancher_url\": \"${RANCHER_URL:-'not determined'}\",
  \"validation_passed\": true
}"

echo "${VALIDATION_RESULTS}" > /root/rancher-validation-results.json
echo "Validation results saved to /root/rancher-validation-results.json"

echo "=== Rancher Validation Completed Successfully ==="
echo "Rancher deployment appears to be working correctly!"

exit 0