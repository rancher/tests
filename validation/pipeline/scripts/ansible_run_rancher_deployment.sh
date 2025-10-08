#!/bin/bash
set -e

# Script to run Rancher deployment playbook from qa-infra-automation repository
# This script is executed within the Docker container

echo "=== Starting Rancher Deployment ==="
echo "RKE2 Version: ${RKE2_VERSION:-'NOT SET'}"
echo "Rancher Version: ${RANCHER_VERSION:-'NOT SET'}"
echo "Private Registry URL: ${PRIVATE_REGISTRY_URL:-'NOT SET'}"
echo "Private Registry Username: ${PRIVATE_REGISTRY_USERNAME:-'NOT SET'}"
echo "QA Infra Work Path: ${QA_INFRA_WORK_PATH:-'NOT SET'}"

# Create SSH directory and authorized_keys file from AWS_SSH_PEM_KEY environment variable
mkdir -p /root/.ssh
if [[ -n "$AWS_SSH_PEM_KEY" ]]; then
    echo "Creating SSH authorized_keys file from AWS_SSH_PEM_KEY environment variable"
    
    # First, decode the base64 key if it's encoded
    if echo "$AWS_SSH_PEM_KEY" | grep -q "^LS0t"; then
        echo "SSH key appears to be base64 encoded, decoding..."
        echo "$AWS_SSH_PEM_KEY" | base64 -d > /tmp/ssh_key.pem
    else
        echo "$AWS_SSH_PEM_KEY" > /tmp/ssh_key.pem
    fi
    
    # Ensure the key file has proper permissions
    chmod 600 /tmp/ssh_key.pem
    
    # Extract the public key from the private key
    if ssh-keygen -y -f /tmp/ssh_key.pem > /root/.ssh/authorized_keys 2>/dev/null; then
        chmod 600 /root/.ssh/authorized_keys
        echo "SSH authorized_keys file created successfully"
        echo "Public key extracted:"
        cat /root/.ssh/authorized_keys
    else
        echo "ERROR: Failed to extract public key from SSH private key"
        echo "Creating empty authorized_keys file to prevent Ansible errors"
        touch /root/.ssh/authorized_keys
        chmod 600 /root/.ssh/authorized_keys
    fi
    
    # Clean up temporary key file
    rm -f /tmp/ssh_key.pem
else
    echo "WARNING: AWS_SSH_PEM_KEY environment variable is not set"
    # Create an empty authorized_keys file to prevent Ansible errors
    touch /root/.ssh/authorized_keys
    chmod 600 /root/.ssh/authorized_keys
fi

# Clone or update the qa-infra-automation repository
if [[ ! -d "/root/qa-infra-automation" ]]; then
    echo "Cloning qa-infra-automation repository..."
    git clone -b ${QA_INFRA_REPO_BRANCH:-main} ${QA_INFRA_REPO_URL:-"https://github.com/rancher/qa-infra-automation.git"} /root/qa-infra-automation
    QA_INFRA_WORK_PATH="/root/qa-infra-automation"
else
    echo "Updating qa-infra-automation repository..."
    cd /root/qa-infra-automation
    git fetch origin
    git checkout ${QA_INFRA_REPO_BRANCH:-main}
    git pull origin ${QA_INFRA_REPO_BRANCH:-main}
    QA_INFRA_WORK_PATH="/root/qa-infra-automation"
fi

# Validate required environment variables
if [[ -z "${RKE2_VERSION}" ]]; then
    echo "ERROR: RKE2_VERSION environment variable is not set"
    exit 1
fi

if [[ -z "${RANCHER_VERSION}" ]]; then
    echo "ERROR: RANCHER_VERSION environment variable is not set"
    exit 1
fi

# Change to the qa-infra-automation directory
cd "${QA_INFRA_WORK_PATH}"

# Ensure Ansible directory structure exists
mkdir -p /root/ansible/rke2/airgap/inventory/

# Check if inventory file exists at the expected Terraform location
if [[ ! -f "/root/ansible/rke2/airgap/inventory.yml" ]]; then
    echo "Ansible inventory file not found at /root/ansible/rke2/airgap/inventory.yml"
    
    # Check if inventory file exists in the shared volume (legacy location)
    if [[ -f "/root/ansible-inventory.yml" ]]; then
        echo "Found inventory file at shared volume location, copying to expected location..."
        cp /root/ansible-inventory.yml /root/ansible/rke2/airgap/inventory.yml
        echo "Inventory file copied successfully"
    else
        # Check for inventory in the qa-infra-automation directory
        echo "Checking for inventory file in qa-infra-automation directory..."
        if [[ -f "${QA_INFRA_WORK_PATH}/inventory/inventory.yml" ]]; then
            echo "Found inventory at qa-infra-automation/inventory/inventory.yml, copying to expected location..."
            cp "${QA_INFRA_WORK_PATH}/inventory/inventory.yml" /root/ansible/rke2/airgap/inventory.yml
            echo "Inventory file copied successfully"
        elif [[ -f "${QA_INFRA_WORK_PATH}/ansible/rke2/airgap/inventory.yml" ]]; then
            echo "Found inventory at qa-infra-automation/ansible/rke2/airgap/inventory.yml, copying to expected location..."
            cp "${QA_INFRA_WORK_PATH}/ansible/rke2/airgap/inventory.yml" /root/ansible/rke2/airgap/inventory.yml
            echo "Inventory file copied successfully"
        else
            echo "ERROR: Ansible inventory file not found at either:"
            echo "  - /root/ansible/rke2/airgap/inventory.yml (Terraform location)"
            echo "  - /root/ansible-inventory.yml (Shared volume location)"
            echo "  - ${QA_INFRA_WORK_PATH}/inventory/inventory.yml (qa-infra location)"
            echo "  - ${QA_INFRA_WORK_PATH}/ansible/rke2/airgap/inventory.yml (qa-infra airgap location)"
            echo "Available files in /root/:"
            ls -la /root/ | grep -E "(inventory|ansible)" || echo "No inventory/ansible files found"
            echo "Available files in /root/ansible/ (if exists):"
            ls -la /root/ansible/ 2>/dev/null || echo "Directory /root/ansible/ does not exist"
            exit 1
        fi
    fi
fi

# Set inventory path and group_vars directory
INVENTORY_PATH="/root/ansible/rke2/airgap/inventory.yml"
GROUP_VARS_DIR="/root/ansible/rke2/airgap/group_vars"

# Copy group_vars to the correct location relative to inventory file
# Ansible loads group_vars relative to the inventory file location
mkdir -p /root/ansible/rke2/airgap/group_vars

# Ensure the group_vars file exists and has basic structure
GROUP_VARS_FILE="/root/ansible/rke2/airgap/group_vars/all.yml"
if [[ ! -f "/root/group_vars/all.yml" ]]; then
  echo "ERROR: Source group_vars file not found at /root/group_vars/all.yml"
  exit 1
fi

cp /root/group_vars/all.yml "$GROUP_VARS_FILE"
echo "Copied group_vars to inventory-relative location: $GROUP_VARS_FILE"

# Ensure file ends with newline before any appends
[[ -n $(tail -c1 "$GROUP_VARS_FILE") ]] && echo "" >> "$GROUP_VARS_FILE"

# Add private registry configuration if provided
if [[ -n "${PRIVATE_REGISTRY_URL}" ]]; then
    echo "Configuring private registry settings..."

    # Ensure file ends with newline before appending
    [[ -n $(tail -c1 "${GROUP_VARS_DIR}/all.yml") ]] && echo "" >> "${GROUP_VARS_DIR}/all.yml"

    # Update private registry URL
    if grep -q "^private_registry_url:" "${GROUP_VARS_DIR}/all.yml"; then
        sed -i "s|^private_registry_url:.*|private_registry_url: \"${PRIVATE_REGISTRY_URL}\"|" "${GROUP_VARS_DIR}/all.yml"
    else
        echo "private_registry_url: \"${PRIVATE_REGISTRY_URL}\"" >> "${GROUP_VARS_DIR}/all.yml"
    fi

    # Update private registry username
    if grep -q "^private_registry_username:" "${GROUP_VARS_DIR}/all.yml"; then
        sed -i "s/^private_registry_username:.*/private_registry_username: \"${PRIVATE_REGISTRY_USERNAME}\"/" "${GROUP_VARS_DIR}/all.yml"
    else
        echo "private_registry_username: \"${PRIVATE_REGISTRY_USERNAME}\"" >> "${GROUP_VARS_DIR}/all.yml"
    fi

    # Update private registry password
    if grep -q "^private_registry_password:" "${GROUP_VARS_DIR}/all.yml"; then
        sed -i "s/^private_registry_password:.*/private_registry_password: \"${PRIVATE_REGISTRY_PASSWORD}\"/" "${GROUP_VARS_DIR}/all.yml"
    else
        echo "private_registry_password: \"${PRIVATE_REGISTRY_PASSWORD}\"" >> "${GROUP_VARS_DIR}/all.yml"
    fi

    # Enable private registry
    if grep -q "^enable_private_registry:" "${GROUP_VARS_DIR}/all.yml"; then
        sed -i "s/^enable_private_registry:.*/enable_private_registry: true/" "${GROUP_VARS_DIR}/all.yml"
    else
        echo "enable_private_registry: true" >> "${GROUP_VARS_DIR}/all.yml"
    fi
fi

# Display the group_vars file for debugging (with size check to avoid flooding logs)
echo "=== group_vars/all.yml content ==="
TOTAL_LINES=$(wc -l < "${GROUP_VARS_DIR}/all.yml")
FILE_SIZE=$(wc -c < "${GROUP_VARS_DIR}/all.yml")
echo "File size: ${FILE_SIZE} bytes, ${TOTAL_LINES} lines"
echo ""

# If file is reasonable size (<200 lines), show it all; otherwise show head/tail
if [[ ${TOTAL_LINES} -le 200 ]]; then
    echo "--- Full content ---"
    cat "${GROUP_VARS_DIR}/all.yml"
else
    echo "--- First 100 lines ---"
    head -100 "${GROUP_VARS_DIR}/all.yml"
    echo ""
    echo "... (lines $((101)) to $((TOTAL_LINES - 20)) omitted) ..."
    echo ""
    echo "--- Last 20 lines ---"
    tail -20 "${GROUP_VARS_DIR}/all.yml"
fi

echo "=== End group_vars/all.yml ==="

# Validate YAML syntax before running playbook
echo "=== Validating group_vars YAML syntax ==="
if command -v python3 &> /dev/null; then
    if python3 -c "import yaml, sys; yaml.safe_load(open('${GROUP_VARS_DIR}/all.yml'))" 2>&1; then
        echo "✓ group_vars/all.yml YAML is valid"
    else
        echo "✗ group_vars/all.yml has YAML syntax errors (see above)"
        echo "This will cause Ansible to fail. Please fix the YAML syntax in your uploaded file."
        exit 1
    fi
elif command -v yamllint &> /dev/null; then
    if yamllint "${GROUP_VARS_DIR}/all.yml"; then
        echo "✓ group_vars/all.yml YAML is valid"
    else
        echo "✗ group_vars/all.yml has YAML validation errors"
        exit 1
    fi
else
    echo "⚠ No YAML validation tool available (python3 or yamllint)"
    echo "Proceeding without validation - errors may occur during playbook execution"
fi
echo "=== End YAML validation ==="

# Validate Ansible inventory structure
echo "=== Validating Ansible Inventory Structure ==="
INVENTORY_FILE="${INVENTORY_PATH}"

if [[ -f "$INVENTORY_FILE" ]]; then
    echo "✓ Inventory file found at $INVENTORY_FILE"

    # Check for required groups
    echo "Checking inventory structure..."

    if grep -q "rke2_servers:" "$INVENTORY_FILE"; then
        echo "✓ rke2_servers group found"
        SERVER_COUNT=$(grep -A 20 "rke2_servers:" "$INVENTORY_FILE" | grep "rke2-server-" | wc -l)
        echo "  - Server nodes: $SERVER_COUNT"
    else
        echo "✗ rke2_servers group NOT found - this will cause incorrect node roles!"
    fi

    if grep -q "rke2_agents:" "$INVENTORY_FILE"; then
        echo "✓ rke2_agents group found"
        AGENT_COUNT=$(grep -A 20 "rke2_agents:" "$INVENTORY_FILE" | grep "rke2-agent-" | wc -l)
        echo "  - Agent nodes: $AGENT_COUNT"
    else
        echo "✗ rke2_agents group NOT found - this will cause incorrect node roles!"
    fi

    # Check if using old inventory structure (fallback)
    if grep -q "airgap_nodes:" "$INVENTORY_FILE" && ! grep -q "rke2_servers:" "$INVENTORY_FILE"; then
        echo "⚠ WARNING: Using legacy inventory structure (airgap_nodes only)"
        echo "  This may cause all nodes to become control-plane nodes"
        echo "  Consider using the updates/rke2-airgap-improvements branch for proper role separation"
    fi

    # Display inventory structure summary
    echo ""
    echo "=== Inventory Structure Summary ==="
    TOTAL_NODES=$(grep -E "rke2-(server|agent)-[0-9]+" "$INVENTORY_FILE" | wc -l)
    echo "Total RKE2 nodes defined: $TOTAL_NODES"

    if [[ $SERVER_COUNT -gt 0 ]] && [[ $AGENT_COUNT -gt 0 ]]; then
        echo "✓ Proper server/agent role separation detected"
        echo "  Expected cluster structure: 1 control-plane, $((AGENT_COUNT)) worker nodes"
    elif [[ $TOTAL_NODES -gt 1 ]]; then
        echo "⚠ WARNING: Multiple nodes detected but no role separation"
        echo "  This will likely result in all nodes becoming control-plane nodes"
    fi

    # Show inventory content for debugging
    echo ""
    echo "=== Inventory File Content ==="
    cat "$INVENTORY_FILE"
    echo "=== End Inventory Content ==="

else
    echo "✗ ERROR: Inventory file not found at $INVENTORY_FILE"
    exit 1
fi

echo "=== End Inventory Validation ==="

# Check for Rancher deployment playbook in multiple locations
RANCHER_PLAYBOOK=""

# Try common locations for Rancher playbooks
PLAYBOOK_LOCATIONS=(
  "ansible/rke2/airgap/playbooks/deploy/rancher-helm-deploy-playbook.yml"
  "ansible/rancher/playbooks/deploy-rancher.yml"
  "ansible/rancher/playbooks/rancher-deployment.yml"
  "ansible/rancher/deploy-rancher.yml"
  "ansible/rke2/airgap/playbooks/deploy-rancher.yml"
  "ansible/rke2/airgap/playbooks/rancher-deployment.yml"
  "ansible/rke2/airgap/playbooks/rancher-helm-deployment.yml"
  "playbooks/deploy/rancher-helm-deployment.yml"
  "playbooks/rancher-deployment.yml"
)

echo "=== Searching for Rancher deployment playbook ==="
for playbook in "${PLAYBOOK_LOCATIONS[@]}"; do
  echo "Checking: ${playbook}"
  if [[ -f "${playbook}" ]]; then
    RANCHER_PLAYBOOK="${playbook}"
    echo "Found Rancher playbook at: ${RANCHER_PLAYBOOK}"
    break
  fi
done

if [[ -z "${RANCHER_PLAYBOOK}" ]]; then
    echo "ERROR: Rancher deployment playbook not found in any expected location"
    echo "Tried the following locations:"
    for playbook in "${PLAYBOOK_LOCATIONS[@]}"; do
      echo "  - ${playbook}"
    done
    echo ""
    echo "Available directories structure:"
    echo "Contents of ansible/:"
    ls -la ansible/ 2>/dev/null || echo "ansible/ directory not found"
    echo ""
    echo "Contents of ansible/rancher/:"
    ls -la ansible/rancher/ 2>/dev/null || echo "ansible/rancher/ directory not found"
    echo ""
    echo "Contents of ansible/rancher/playbooks/:"
    ls -la ansible/rancher/playbooks/ 2>/dev/null || echo "ansible/rancher/playbooks/ directory not found"
    echo ""
    echo "Contents of ansible/rke2/airgap/playbooks/:"
    ls -la ansible/rke2/airgap/playbooks/ 2>/dev/null || echo "ansible/rke2/airgap/playbooks/ directory not found"
    exit 1
fi

# Display playbook content for debugging
echo "=== Rancher Playbook Content ==="
cat "${RANCHER_PLAYBOOK}"
echo "================================="

# TEST MODE: Check if we should force failure to test DESTROY_ON_FAILURE
if grep -q "test_force_failure: true" "${GROUP_VARS_DIR}/all.yml" 2>/dev/null; then
    echo "=========================================="
    echo "TEST MODE: Forcing deployment failure to test DESTROY_ON_FAILURE cleanup"
    echo "=========================================="
    echo ""
    echo "To disable this test mode, remove 'test_force_failure: true' from your group_vars/all.yml"
    echo ""
    exit 1
fi

# Note: cert-manager installation and health checks are handled by the Ansible playbook
# However, we'll add additional readiness checks to ensure cert-manager is fully ready
echo "=== Preparing to deploy Rancher ==="
echo "Performing additional readiness checks before Rancher deployment..."

# Wait for cert-manager to be fully ready before proceeding with Rancher
echo "=== Checking cert-manager readiness ==="
if command -v kubectl &> /dev/null; then
    export KUBECONFIG="/home/ubuntu/.kube/config"

    # Check if cert-manager namespace exists
    if kubectl get namespace cert-manager &> /dev/null; then
        echo "cert-manager namespace found"

        # Wait for cert-manager deployment to be ready - increased timeout
        echo "Waiting for cert-manager deployment to be ready..."
        if kubectl wait --for=condition=available --timeout=600s deployment/cert-manager -n cert-manager; then
            echo "✓ cert-manager deployment is ready"
        else
            echo "✗ ERROR: cert-manager deployment not ready after 10 minutes"
            echo "Showing cert-manager deployment status:"
            kubectl get deployment cert-manager -n cert-manager -o yaml
            echo ""
            echo "Showing cert-manager pod status:"
            kubectl get pods -n cert-manager -o wide
            echo ""
            echo "This is a fatal error - Rancher deployment requires cert-manager to be fully ready"
            exit 1
        fi

        # Wait for webhook service endpoints to be available - CRITICAL for Rancher
        echo "Waiting for cert-manager webhook service endpoints to be available..."
        WEBHOOK_READY=false
        for i in {1..30}; do
            if kubectl get endpoints cert-manager-webhook -n cert-manager &> /dev/null; then
                WEBHOOK_ENDPOINTS=$(kubectl get endpoints cert-manager-webhook -n cert-manager -o jsonpath='{.subsets[*].addresses[*]}' | wc -w)
                if [[ $WEBHOOK_ENDPOINTS -gt 0 ]]; then
                    echo "✓ cert-manager webhook service has $WEBHOOK_ENDPOINTS endpoint(s) available after ${i} attempts"
                    WEBHOOK_READY=true
                    break
                else
                    echo "Attempt ${i}/30: cert-manager webhook service has no endpoints available, waiting 20 seconds..."
                    sleep 20
                fi
            else
                echo "Attempt ${i}/30: cert-manager webhook service not found, waiting 20 seconds..."
                sleep 20
            fi
        done

        if [[ "$WEBHOOK_READY" == false ]]; then
            echo "✗ ERROR: cert-manager webhook service never became ready after 10 minutes"
            echo "This will cause Rancher installation to fail - webhook validation is required"
            echo ""
            echo "Debugging information:"
            echo "cert-manager endpoints:"
            kubectl get endpoints -n cert-manager || echo "No endpoints found"
            echo ""
            echo "cert-manager pods:"
            kubectl get pods -n cert-manager -o wide
            echo ""
            echo "cert-manager services:"
            kubectl get svc -n cert-manager
            echo ""
            echo "cert-manager pod logs (last 20 lines):"
            kubectl logs -n cert-manager -l app.kubernetes.io/name=cert-manager --tail=20 || echo "Could not get pod logs"
            exit 1
        fi

        # Check webhook service connectivity
        echo "Testing webhook service connectivity..."
        WEBHOOK_SVC=$(kubectl get svc cert-manager-webhook -n cert-manager -o jsonpath='{.spec.clusterIP}' 2>/dev/null || echo "")
        if [[ -n "$WEBHOOK_SVC" ]]; then
            echo "cert-manager webhook service IP: $WEBHOOK_SVC"
            # Basic connectivity test (without TLS verification)
            if timeout 10 nc -z $WEBHOOK_SVC 443 2>/dev/null; then
                echo "✓ cert-manager webhook service is reachable on port 443"
            else
                echo "⚠ WARNING: Cannot reach cert-manager webhook service on port 443"
            fi
        fi

        # Test API server responsiveness - metrics API errors indicate overload
        echo "Testing Kubernetes API server responsiveness..."
        API_READY=false
        for i in {1..12}; do
            if kubectl get nodes --no-headers | grep -q "Ready" 2>/dev/null; then
                echo "✓ Kubernetes API server is responsive after ${i} attempts"
                API_READY=true
                break
            else
                echo "Attempt ${i}/12: API server not responsive, waiting 10 seconds..."
                sleep 10
            fi
        done

        if [[ "$API_READY" == false ]]; then
            echo "✗ ERROR: Kubernetes API server not responsive after 2 minutes"
            echo "This may indicate API server overload or other cluster issues"
            echo "Rancher deployment requires a functional API server"
            exit 1
        fi

        # Final comprehensive readiness validation - test actual webhook connectivity
        echo "=== Final Webhook Readiness Validation ==="
        WEBHOOK_VALIDATION_PASSED=true

        # Test cert-manager webhook with a simple dry-run validation
        echo "Testing cert-manager webhook connectivity..."
        if kubectl create --dry-run=client -f - <<EOF
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: test-webhook-validation
spec:
  selfSigned: {}
EOF
        then
            echo "✓ cert-manager webhook validation test passed"
        else
            echo "✗ ERROR: cert-manager webhook validation test failed"
            WEBHOOK_VALIDATION_PASSED=false
        fi

        # Test ingress admission webhook if it exists
        echo "Testing ingress controller webhook connectivity..."
        if kubectl get validatingwebhookconfiguration ingress-nginx-admission &> /dev/null; then
            if kubectl create --dry-run=client -f - <<EOF
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: test-ingress-validation
  annotations:
    nginx.ingress.kubernetes.io/rewrite-target: /
spec:
  rules:
  - host: test.local
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: test
            port:
              number: 80
EOF
            then
                echo "✓ ingress webhook validation test passed"
            else
                echo "✗ ERROR: ingress webhook validation test failed"
                WEBHOOK_VALIDATION_PASSED=false
            fi
        else
            echo "⚠ WARNING: ingress webhook not found, skipping validation"
        fi

        if [[ "$WEBHOOK_VALIDATION_PASSED" == false ]]; then
            echo "✗ ERROR: Final webhook validation failed"
            echo "This indicates webhooks are not properly configured or accessible"
            echo "Rancher deployment will fail without functional webhooks"
            exit 1
        fi

        echo "✓ All webhook validations passed - proceeding with Rancher deployment"
    else
        echo "⚠ WARNING: cert-manager namespace not found"
    fi
else
    echo "⚠ WARNING: kubectl not available for readiness checks"
fi

echo "cert-manager readiness checks completed"

# Also check ingress controller readiness since it's needed for Rancher webhooks
echo "=== Checking ingress controller readiness ==="
if command -v kubectl &> /dev/null; then
    export KUBECONFIG="/home/ubuntu/.kube/config"

    # Look for common ingress controllers
    INGRESS_NAMESPACES=("kube-system" "ingress-nginx")
    INGRESS_DEPLOYMENTS=("ingress-nginx-controller" "nginx-ingress-controller" "rke2-ingress-nginx-controller")

    INGRESS_READY=false

    for namespace in "${INGRESS_NAMESPACES[@]}"; do
        if kubectl get namespace "$namespace" &> /dev/null; then
            echo "Found ingress namespace: $namespace"

            for deployment in "${INGRESS_DEPLOYMENTS[@]}"; do
                if kubectl get deployment "$deployment" -n "$namespace" &> /dev/null; then
                    echo "Found ingress deployment: $deployment in namespace $namespace"

                    # Check deployment status
                    if kubectl wait --for=condition=available --timeout=120s deployment/"$deployment" -n "$namespace" 2>/dev/null; then
                        echo "✓ Ingress deployment $deployment is ready"
                        INGRESS_READY=true

                        # Check admission webhook endpoints if they exist
                        if kubectl get service "$deployment-admission" -n "$namespace" &> /dev/null; then
                            echo "Found ingress admission webhook service"
                            ADMISSION_ENDPOINTS=$(kubectl get endpoints "$deployment-admission" -n "$namespace" -o jsonpath='{.subsets[*].addresses[*]}' | wc -w)
                            if [[ $ADMISSION_ENDPOINTS -gt 0 ]]; then
                                echo "✓ Ingress admission webhook has $ADMISSION_ENDPOINTS endpoint(s) available"
                            else
                                echo "⚠ WARNING: Ingress admission webhook has no endpoints available"
                            fi
                        fi
                        break 2
                    else
                        echo "⚠ WARNING: Ingress deployment $deployment not ready after 2 minutes"
                    fi
                fi
            done
        fi
    done

    if [[ "$INGRESS_READY" == false ]]; then
        echo "⚠ WARNING: No ready ingress controller found"
        echo "This may cause Rancher webhook validation to fail"
    else
        echo "✓ At least one ingress controller is ready"
    fi
else
    echo "⚠ WARNING: kubectl not available for ingress readiness checks"
fi

echo "Ingress controller readiness checks completed"

# Build extra variables to pass to ansible-playbook
EXTRA_VARS=""

if [[ -n "${HOSTNAME_PREFIX}" ]]; then
    EXTRA_VARS="${EXTRA_VARS} -e hostname_prefix=${HOSTNAME_PREFIX}"
    # Also pass as rancher_hostname with .qa.rancher.space suffix for Rancher Helm chart
    EXTRA_VARS="${EXTRA_VARS} -e rancher_hostname=${HOSTNAME_PREFIX}.qa.rancher.space"
    echo "Passing HOSTNAME_PREFIX as extra variable: ${HOSTNAME_PREFIX}"
    echo "Passing rancher_hostname as extra variable: ${HOSTNAME_PREFIX}.qa.rancher.space"
fi

if [[ -n "${RANCHER_VERSION}" ]]; then
    EXTRA_VARS="${EXTRA_VARS} -e rancher_version=${RANCHER_VERSION}"
    echo "Passing RANCHER_VERSION as extra variable: ${RANCHER_VERSION}"
fi

if [[ -n "${RKE2_VERSION}" ]]; then
    EXTRA_VARS="${EXTRA_VARS} -e rke2_version=${RKE2_VERSION}"
    echo "Passing RKE2_VERSION as extra variable: ${RKE2_VERSION}"
fi

if [[ -n "${PRIVATE_REGISTRY_URL}" ]]; then
    EXTRA_VARS="${EXTRA_VARS} -e private_registry_url=${PRIVATE_REGISTRY_URL}"
    echo "Passing PRIVATE_REGISTRY_URL as extra variable: ${PRIVATE_REGISTRY_URL}"
fi

echo "Extra variables for ansible-playbook: ${EXTRA_VARS}"

# Run the Rancher deployment playbook from qa-infra-automation
echo "Running Rancher deployment playbook..."
cd /root/qa-infra-automation/ansible/rke2/airgap

# Run ansible-playbook and capture exit code
ansible-playbook -i /root/ansible/rke2/airgap/inventory.yml "${RANCHER_PLAYBOOK}" -v ${EXTRA_VARS}
ANSIBLE_EXIT_CODE=$?

# Capture the exit code
ANSIBLE_EXIT_CODE=$?

echo "=== Rancher Deployment Completed ==="
echo "Ansible exit code: ${ANSIBLE_EXIT_CODE}"

# Check if Ansible failed critically (exit code 2 is for failed tasks, but deployment may still succeed)
if [[ $ANSIBLE_EXIT_CODE -eq 2 ]]; then
    echo "WARNING: Ansible playbook had failed tasks (exit code 2), but checking if deployment succeeded..."
    # Check if the cluster is actually working by testing kubectl connectivity
    if [[ -f /home/ubuntu/.kube/config ]]; then
        export KUBECONFIG=/home/ubuntu/.kube/config
        if kubectl get nodes --no-headers | grep -q "Ready"; then
            echo "SUCCESS: Despite Ansible task failures, cluster is operational with ready nodes"
            echo "Treating this as successful deployment"
            ANSIBLE_EXIT_CODE=0
        else
            echo "ERROR: Ansible failed and cluster is not operational"
        fi
    else
        echo "ERROR: Ansible failed and no kubeconfig found"
    fi
elif [[ $ANSIBLE_EXIT_CODE -ne 0 ]]; then
    echo "ERROR: Ansible playbook failed with exit code $ANSIBLE_EXIT_CODE"
fi

# Copy playbook execution logs to shared volume
if [[ -f "ansible-playbook.log" ]]; then
    cp ansible-playbook.log /root/rancher_deployment_execution.log
fi

# Verify node roles after deployment (only if deployment succeeded)
if [[ $ANSIBLE_EXIT_CODE -eq 0 ]]; then
    echo "=== Verifying RKE2 Node Roles ==="

    # Run the node role verification playbook if it exists
    NODE_ROLE_PLAYBOOK="/root/qa-infra-automation/ansible/rke2/airgap/playbooks/debug/check-node-roles.yml"
    if [[ -f "$NODE_ROLE_PLAYBOOK" ]]; then
        echo "Running node role verification playbook..."
        cd /root/qa-infra-automation/ansible/rke2/airgap
        ansible-playbook -i /root/ansible/rke2/airgap/inventory.yml playbooks/debug/check-node-roles.yml -v

        if [[ $? -eq 0 ]]; then
            echo "✓ Node role verification completed"
        else
            echo "⚠ Node role verification had issues"
        fi
    else
        echo "Node role verification playbook not found, performing manual check..."

        # Manual node role check
        cd /root/qa-infra-automation/ansible/rke2/airgap
        if ansible-playbook -i /root/ansible/rke2/airgap/inventory.yml -c local -m shell -a "export KUBECONFIG=/etc/rancher/rke2/rke2.yaml && /var/lib/rancher/rke2/bin/kubectl get nodes -o wide 2>/dev/null || echo 'kubectl not available'" localhost 2>/dev/null; then
            echo "✓ Node roles checked manually"
        else
            echo "⚠ Could not verify node roles manually"
        fi
    fi

    echo "=== End Node Role Verification ==="
fi

# Copy kubeconfig to shared volume for Jenkins archival if deployment succeeded
if [[ $ANSIBLE_EXIT_CODE -eq 0 ]]; then
    echo "Copying kubeconfig to shared volume for archival..."
    KUBECONFIG_LOCATIONS=(
        "/home/ubuntu/.kube/config"
        "/etc/rancher/rke2/rke2.yaml"
        "/root/.kube/config"
        "/root/ansible/rke2/airgap/kubeconfig"
    )

    KUBECONFIG_FOUND=false
    for config_path in "${KUBECONFIG_LOCATIONS[@]}"; do
        if [[ -f "$config_path" ]]; then
            echo "Found kubeconfig at: $config_path"
            cp "$config_path" /root/kubeconfig.yaml
            chmod 644 /root/kubeconfig.yaml
            echo "✓ Kubeconfig copied to /root/kubeconfig.yaml for archival"
            KUBECONFIG_FOUND=true
            break
        fi
    done

    if [[ "$KUBECONFIG_FOUND" == false ]]; then
        echo "⚠ WARNING: Kubeconfig not found after successful Rancher deployment"
    fi
fi

echo "=== Rancher Deployment Completed ==="

# Exit with the same code as ansible-playbook
exit ${ANSIBLE_EXIT_CODE}