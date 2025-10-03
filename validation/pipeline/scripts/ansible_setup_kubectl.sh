#!/bin/bash
set -e

# Ansible Kubectl Access Setup Script
# This script sets up kubectl access on the bastion host using playbook from qa-infra-automation

echo "=== Ansible Kubectl Access Setup Started ==="

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
        echo "ERROR: Ansible inventory file not found at either:"
        echo "  - /root/ansible/rke2/airgap/inventory.yml (Terraform location)"
        echo "  - /root/ansible-inventory.yml (Shared volume location)"
        echo "Available files in /root/:"
        ls -la /root/ | grep -E "(inventory|ansible)" || echo "No inventory/ansible files found"
        echo "Available files in /root/ansible/ (if exists):"
        ls -la /root/ansible/ 2>/dev/null || echo "Directory /root/ansible/ does not exist"
        exit 1
    fi
fi

if [[ ! -f "/root/group_vars/all.yml" ]]; then
    echo "ERROR: Ansible group_vars file not found at /root/group_vars/all.yml"
    exit 1
fi

# Clone or update the qa-infra-automation repository
if [[ ! -d "/root/qa-infra-automation" ]]; then
    echo "Cloning qa-infra-automation repository..."
    git clone ${QA_INFRA_REPO_URL:-"https://github.com/rancher/qa-infra-automation.git"} /root/qa-infra-automation
else
    echo "Updating qa-infra-automation repository..."
    cd /root/qa-infra-automation
    git pull origin main
    cd /root
fi

# Copy group_vars to the correct location relative to inventory file
mkdir -p /root/ansible/rke2/airgap/group_vars
cp /root/group_vars/all.yml /root/ansible/rke2/airgap/group_vars/all.yml
echo "Copied group_vars to inventory-relative location"

# Ensure the kubectl setup playbook exists
KUBECTL_SETUP_PLAYBOOK="/root/qa-infra-automation/ansible/rke2/airgap/playbooks/setup/setup-kubectl-access.yml"
if [[ ! -f "$KUBECTL_SETUP_PLAYBOOK" ]]; then
    echo "ERROR: Kubectl setup playbook not found at $KUBECTL_SETUP_PLAYBOOK"
    echo "Available setup playbooks:"
    ls -la "/root/qa-infra-automation/ansible/rke2/airgap/playbooks/setup/" 2>/dev/null || echo "Setup directory not found"
    exit 1
fi

echo "Using kubectl access setup playbook from qa-infra-automation repository"
echo "Playbook path: $KUBECTL_SETUP_PLAYBOOK"

# Run the kubectl access setup playbook from qa-infra-automation
echo "Running kubectl access setup playbook..."
cd /root/qa-infra-automation/ansible/rke2/airgap

# Pass RKE2_VERSION as an extra variable to ensure it's available
EXTRA_VARS=""
if [[ -n "${RKE2_VERSION}" ]]; then
    EXTRA_VARS="-e rke2_version=${RKE2_VERSION}"
    echo "Passing RKE2_VERSION as extra variable: ${RKE2_VERSION}"
fi

ansible-playbook -i /root/ansible/rke2/airgap/inventory.yml playbooks/setup/setup-kubectl-access.yml -v ${EXTRA_VARS}

echo "Kubectl access setup playbook execution completed"

# Copy playbook execution logs to shared volume
if [[ -f "ansible-playbook.log" ]]; then
    cp ansible-playbook.log /root/kubectl_access_setup_execution.log
fi

# Copy kubeconfig to shared volume for Jenkins archival
echo "Copying kubeconfig to shared volume for archival..."
KUBECONFIG_LOCATIONS=(
    "/root/.kube/config"
    "/etc/rancher/rke2/rke2.yaml"
    "/root/ansible/rke2/airgap/kubeconfig"
    "/tmp/kubeconfig.yaml"
)

KUBECONFIG_FOUND=false
for config_path in "${KUBECONFIG_LOCATIONS[@]}"; do
    if [[ -f "$config_path" ]]; then
        echo "Found kubeconfig at: $config_path"
        cp "$config_path" /root/kubeconfig.yaml
        chmod 644 /root/kubeconfig.yaml

        # Fix kubeconfig to use bastion IP instead of localhost
        # This is critical for Docker container access to the cluster
        echo "Updating kubeconfig to use bastion IP instead of localhost..."

        # Show original kubeconfig server URL for debugging
        echo "Original kubeconfig server URL:"
        grep "server:" /root/kubeconfig.yaml || echo "Could not find server line"

        # Extract bastion node IP from inventory - try multiple methods
        BASTION_IP=""

        # Method 1: Look for bastion-node in inventory
        if [[ -f "/root/ansible/rke2/airgap/inventory.yml" ]]; then
            BASTION_IP=$(grep -A 10 "bastion-node:" /root/ansible/rke2/airgap/inventory.yml | grep "ansible_host:" | head -1 | awk '{print $2}' | tr -d '"' | tr -d "'" || echo "")
        fi

        # Method 2: Try to get from hostname
        if [[ -z "${BASTION_IP}" ]]; then
            echo "Method 1 failed, trying hostname -I..."
            BASTION_IP=$(hostname -I | awk '{print $1}' || echo "")
        fi

        # Method 3: Try to get from ip addr
        if [[ -z "${BASTION_IP}" ]]; then
            echo "Method 2 failed, trying ip addr..."
            BASTION_IP=$(ip addr show | grep 'inet ' | grep -v '127.0.0.1' | head -1 | awk '{print $2}' | cut -d'/' -f1 || echo "")
        fi

        # Method 4: Try to extract from current kubeconfig if it has a valid IP
        if [[ -z "${BASTION_IP}" ]]; then
            echo "Method 3 failed, checking if kubeconfig already has a valid IP..."
            CURRENT_SERVER=$(grep "server:" /root/kubeconfig.yaml | awk '{print $2}' || echo "")
            if [[ "${CURRENT_SERVER}" =~ ^https://[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+:6443$ ]]; then
                BASTION_IP=$(echo "${CURRENT_SERVER}" | sed 's|https://||' | sed 's|:6443||')
                echo "Found existing valid IP in kubeconfig: ${BASTION_IP}"
            fi
        fi

        if [[ -n "${BASTION_IP}" ]]; then
            echo "Using bastion IP: ${BASTION_IP}"

            # Replace any server URL with the correct one
            # This handles localhost, template variables, or any malformed URLs
            sed -i "s|server:.*|server: https://${BASTION_IP}:6443|" /root/kubeconfig.yaml

            echo "✓ Updated kubeconfig server URL to https://${BASTION_IP}:6443"

            # Verify the change
            echo "Kubeconfig server URL after update:"
            grep "server:" /root/kubeconfig.yaml || echo "Failed to verify server URL"
        else
            echo "ERROR: Could not determine bastion IP using any method"
            echo "This will cause connection issues from Docker containers"
            echo "Kubeconfig will be unusable for remote access"
        fi

        echo "✓ Kubeconfig copied to /root/kubeconfig.yaml for archival"
        KUBECONFIG_FOUND=true
        break
    fi
done

if [[ "$KUBECONFIG_FOUND" == false ]]; then
    echo "⚠ WARNING: Kubeconfig not found in any expected location:"
    for config_path in "${KUBECONFIG_LOCATIONS[@]}"; do
        echo "  - $config_path"
    done
    echo "Available files in /root/.kube/:"
    ls -la /root/.kube/ 2>/dev/null || echo "Directory /root/.kube/ does not exist"
fi

echo "=== Ansible Kubectl Access Setup Completed ==="
