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

# Validate required environment variables
if [[ -z "${QA_INFRA_WORK_PATH}" ]]; then
    echo "ERROR: QA_INFRA_WORK_PATH environment variable is not set"
    exit 1
fi

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

# Verify the qa-infra-automation repository structure
if [[ ! -d "ansible/rke2/airgap" ]]; then
    echo "ERROR: ansible/rke2/airgap directory not found in qa-infra-automation repository"
    echo "Contents of $(pwd):"
    ls -la
    exit 1
fi

# Change to the airgap ansible directory
cd ansible/rke2/airgap

# Verify inventory file exists
if [[ ! -f "inventory.yml" ]]; then
    echo "ERROR: inventory.yml not found in ansible/rke2/airgap directory"
    echo "Contents of current directory:"
    ls -la
    exit 1
fi

# Verify group_vars directory exists
if [[ ! -d "group_vars" ]]; then
    echo "ERROR: group_vars directory not found in ansible/rke2/airgap directory"
    echo "Contents of current directory:"
    ls -la
    exit 1
fi

# Verify all.yml exists in group_vars
if [[ ! -f "group_vars/all.yml" ]]; then
    echo "ERROR: group_vars/all.yml not found"
    echo "Contents of group_vars directory:"
    ls -la group_vars/
    exit 1
fi

# Update group_vars/all.yml with Rancher version if not already set
echo "Updating group_vars/all.yml with Rancher configuration..."
if grep -q "^rancher_version:" group_vars/all.yml; then
    sed -i "s/^rancher_version:.*/rancher_version: \"${RANCHER_VERSION}\"/" group_vars/all.yml
else
    echo "rancher_version: \"${RANCHER_VERSION}\"" >> group_vars/all.yml
fi

# Add private registry configuration if provided
if [[ -n "${PRIVATE_REGISTRY_URL}" ]]; then
    echo "Configuring private registry settings..."
    
    # Update private registry URL
    if grep -q "^private_registry_url:" group_vars/all.yml; then
        sed -i "s|^private_registry_url:.*|private_registry_url: \"${PRIVATE_REGISTRY_URL}\"|" group_vars/all.yml
    else
        echo "private_registry_url: \"${PRIVATE_REGISTRY_URL}\"" >> group_vars/all.yml
    fi
    
    # Update private registry username
    if grep -q "^private_registry_username:" group_vars/all.yml; then
        sed -i "s/^private_registry_username:.*/private_registry_username: \"${PRIVATE_REGISTRY_USERNAME}\"/" group_vars/all.yml
    else
        echo "private_registry_username: \"${PRIVATE_REGISTRY_USERNAME}\"" >> group_vars/all.yml
    fi
    
    # Update private registry password
    if grep -q "^private_registry_password:" group_vars/all.yml; then
        sed -i "s/^private_registry_password:.*/private_registry_password: \"${PRIVATE_REGISTRY_PASSWORD}\"/" group_vars/all.yml
    else
        echo "private_registry_password: \"${PRIVATE_REGISTRY_PASSWORD}\"" >> group_vars/all.yml
    fi
    
    # Enable private registry
    if grep -q "^enable_private_registry:" group_vars/all.yml; then
        sed -i "s/^enable_private_registry:.*/enable_private_registry: true/" group_vars/all.yml
    else
        echo "enable_private_registry: true" >> group_vars/all.yml
    fi
fi

# Display the updated configuration
echo "=== Updated group_vars/all.yml ==="
cat group_vars/all.yml
echo "================================="

# Check if Rancher deployment playbook exists
RANCHER_PLAYBOOK="playbooks/deploy/rancher-helm-deployment.yml"
if [[ ! -f "${RANCHER_PLAYBOOK}" ]]; then
    echo "ERROR: Rancher deployment playbook not found at ${RANCHER_PLAYBOOK}"
    echo "Available playbooks in playbooks/deploy:"
    ls -la playbooks/deploy/ || echo "playbooks/deploy directory not found"
    exit 1
fi

# Display playbook content for debugging
echo "=== Rancher Playbook Content ==="
cat "${RANCHER_PLAYBOOK}"
echo "================================="

# Run the Rancher deployment playbook
echo "=== Running Rancher Deployment Playbook ==="
ansible-playbook -i inventory.yml "${RANCHER_PLAYBOOK}" -v

# Capture the exit code
ANSIBLE_EXIT_CODE=$?

echo "=== Rancher Deployment Completed ==="
echo "Ansible exit code: ${ANSIBLE_EXIT_CODE}"

# Save deployment logs
echo "Saving Rancher deployment logs..."
mkdir -p /root
echo "Rancher deployment completed with exit code: ${ANSIBLE_EXIT_CODE}" > /root/rancher-deployment-status.txt

# Exit with the same code as ansible-playbook
exit ${ANSIBLE_EXIT_CODE}