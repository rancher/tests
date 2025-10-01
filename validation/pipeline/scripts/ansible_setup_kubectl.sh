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
KUBECTL_SETUP_PLAYBOOK="/root/qa-infra-automation/ansible/rke2/airgap/playbooks/setup/kubectl-access-setup.yml"
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
ansible-playbook -i /root/ansible/rke2/airgap/inventory.yml playbooks/setup/kubectl-access-setup.yml -v

echo "Kubectl access setup playbook execution completed"

# Copy playbook execution logs to shared volume
if [[ -f "ansible-playbook.log" ]]; then
    cp ansible-playbook.log /root/kubectl_access_setup_execution.log
fi

echo "=== Ansible Kubectl Access Setup Completed ==="