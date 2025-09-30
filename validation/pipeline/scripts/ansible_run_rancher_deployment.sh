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

# Determine inventory path flexibly
AIRGAP_DIR="ansible/rke2/airgap"
INVENTORY_PATH=""
GROUP_VARS_DIR=""

if [[ -f "${AIRGAP_DIR}/inventory.yml" ]]; then
  INVENTORY_PATH="${AIRGAP_DIR}/inventory.yml"
  GROUP_VARS_DIR="${AIRGAP_DIR}/group_vars"
elif [[ -f "inventory/inventory.yml" ]]; then
  INVENTORY_PATH="inventory/inventory.yml"
  GROUP_VARS_DIR="inventory/group_vars"
else
  # Try common inventory file names under inventory/
  for f in inventory/hosts.yml inventory/hosts.yaml inventory/hosts.ini; do
    if [[ -f "$f" ]]; then
      INVENTORY_PATH="$f"
      GROUP_VARS_DIR="inventory/group_vars"
      break
    fi
  done
fi

if [[ -z "${INVENTORY_PATH}" ]]; then
  echo "ERROR: Could not locate an Ansible inventory file. Tried:"
  echo " - ${AIRGAP_DIR}/inventory.yml"
  echo " - inventory/inventory.yml"
  echo " - inventory/hosts.yml(yaml|ini)"
  echo "Repository root contents:"
  ls -la
  exit 1
fi

# Validate group_vars
if [[ -z "${GROUP_VARS_DIR}" || ! -d "${GROUP_VARS_DIR}" ]]; then
  echo "ERROR: group_vars directory not found. Looked for: ${GROUP_VARS_DIR}"
  echo "Contents of repository root and inventory directories:"
  ls -la
  ls -la "$(dirname "${INVENTORY_PATH}")" || true
  exit 1
fi

# Verify all.yml exists in group_vars
if [[ ! -f "${GROUP_VARS_DIR}/all.yml" ]]; then
  echo "ERROR: group_vars/all.yml not found at ${GROUP_VARS_DIR}/all.yml"
  echo "Contents of ${GROUP_VARS_DIR}:"
  ls -la "${GROUP_VARS_DIR}"
  exit 1
fi

# Update group_vars/all.yml with Rancher version if not already set
echo "Updating group_vars/all.yml with Rancher configuration..."
if grep -q "^rancher_version:" "${GROUP_VARS_DIR}/all.yml"; then
    sed -i "s/^rancher_version:.*/rancher_version: \"${RANCHER_VERSION}\"/" "${GROUP_VARS_DIR}/all.yml"
else
    echo "rancher_version: \"${RANCHER_VERSION}\"" >> "${GROUP_VARS_DIR}/all.yml"
fi

# Add private registry configuration if provided
if [[ -n "${PRIVATE_REGISTRY_URL}" ]]; then
    echo "Configuring private registry settings..."
    
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

# Display the updated configuration
echo "=== Updated group_vars/all.yml ==="
cat "${GROUP_VARS_DIR}/all.yml"
echo "================================="

# Check if Rancher deployment playbook exists (from repo root)
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

# Run the Rancher deployment playbook from repository root with detected inventory
echo "=== Running Rancher Deployment Playbook ==="
ansible-playbook -i "${INVENTORY_PATH}" "${RANCHER_PLAYBOOK}" -v

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