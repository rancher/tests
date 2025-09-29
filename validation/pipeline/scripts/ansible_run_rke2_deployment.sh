#!/bin/bash
set -e

# Ansible RKE2 Tarball Deployment Script
# This script clones and runs the RKE2 tarball deployment playbook from qa-infra-automation

echo "=== Ansible RKE2 Tarball Deployment Started ==="

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
    git clone ${QA_INFRA_REPO_URL:-"https://github.com/rancher/qa-infra-automation.git"} /root/qa-infra-automation
else
    echo "Updating qa-infra-automation repository..."
    cd /root/qa-infra-automation
    git pull origin main
fi

# Ensure the required playbooks exist
QA_INFRA_PLAYBOOKS_DIR="/root/qa-infra-automation/ansible/rke2/airgap/playbooks"
if [[ ! -d "$QA_INFRA_PLAYBOOKS_DIR" ]]; then
    echo "ERROR: Playbooks directory not found at $QA_INFRA_PLAYBOOKS_DIR"
    exit 1
fi

# Check for the main RKE2 tarball deployment playbook
RKE2_TARBALL_PLAYBOOK="$QA_INFRA_PLAYBOOKS_DIR/deploy/rke2-tarball-playbook.yml"
if [[ ! -f "$RKE2_TARBALL_PLAYBOOK" ]]; then
    echo "ERROR: RKE2 tarball deployment playbook not found at $RKE2_TARBALL_PLAYBOOK"
    echo "Available playbooks:"
    ls -la "$QA_INFRA_PLAYBOOKS_DIR/"
    echo "Available subdirectories:"
    find "$QA_INFRA_PLAYBOOKS_DIR" -type d -name "*" | head -10
    exit 1
fi

# Copy group_vars to the qa-infra-automation structure if needed
mkdir -p /root/qa-infra-automation/ansible/rke2/airgap/group_vars
cp /root/group_vars/all.yml /root/qa-infra-automation/ansible/rke2/airgap/group_vars/

# Ensure RKE2_VERSION is set in the group_vars file
if [[ -n "${RKE2_VERSION}" ]]; then
    echo "Ensuring RKE2_VERSION is set in group_vars: ${RKE2_VERSION}"
    # Replace or add the rke2_version line in the group_vars file
    sed -i "s/rke2_version:.*/rke2_version: \"${RKE2_VERSION}\"/" /root/qa-infra-automation/ansible/rke2/airgap/group_vars/all.yml
    if ! grep -q "rke2_version:" /root/qa-infra-automation/ansible/rke2/airgap/group_vars/all.yml; then
        echo "rke2_version: \"${RKE2_VERSION}\"" >> /root/qa-infra-automation/ansible/rke2/airgap/group_vars/all.yml
    fi
else
    echo "WARNING: RKE2_VERSION environment variable is not set"
    echo "Setting default RKE2 version"
    sed -i "s/rke2_version:.*/rke2_version: \"v1.28.8+rke2r1\"/" /root/qa-infra-automation/ansible/rke2/airgap/group_vars/all.yml
fi

echo "Using RKE2 tarball deployment playbook from qa-infra-automation repository"
echo "Playbook path: $RKE2_TARBALL_PLAYBOOK"

# Run the RKE2 deployment playbook from qa-infra-automation
echo "Running RKE2 tarball deployment playbook..."
cd /root/qa-infra-automation/ansible/rke2/airgap
ansible-playbook -i /root/ansible/rke2/airgap/inventory.yml playbooks/deploy/rke2-tarball-playbook.yml -v

echo "RKE2 tarball deployment playbook execution completed"

# Copy playbook execution logs to shared volume
if [[ -f "ansible-playbook.log" ]]; then
    cp ansible-playbook.log /root/rke2_tarball_deployment_execution.log
fi

echo "=== Ansible RKE2 Tarball Deployment Completed ==="