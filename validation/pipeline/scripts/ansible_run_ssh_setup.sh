#!/bin/bash
set -e

# Ansible SSH Setup Playbook Script
# This script runs the Ansible SSH setup playbook

echo "=== Ansible SSH Setup Playbook Started ==="

# Debug: Show current directory and file structure
echo "Current working directory: $(pwd)"
echo "Contents of /root/:"
ls -la /root/ | head -20
echo "Contents of /root/ansible/ (if exists):"
ls -la /root/ansible/ 2>/dev/null || echo "Directory /root/ansible/ does not exist"

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

# Check if group_vars file exists at expected location
if [[ ! -f "/root/group_vars/all.yml" ]]; then
    echo "Ansible group_vars file not found at /root/group_vars/all.yml"
    
    # Check if group_vars file exists in /tmp/ (where it's generated)
    if [[ -f "/tmp/group_vars/all.yml" ]]; then
        echo "Found group_vars file at /tmp/group_vars/all.yml, copying to expected location..."
        mkdir -p /root/group_vars
        cp /tmp/group_vars/all.yml /root/group_vars/all.yml
        echo "Group_vars file copied successfully"
    else
        echo "ERROR: Ansible group_vars file not found at either:"
        echo "  - /root/group_vars/all.yml (expected location)"
        echo "  - /tmp/group_vars/all.yml (generation location)"
        echo "Available files in /tmp/:"
        ls -la /tmp/ | grep -E "(group|vars)" || echo "No group_vars files found in /tmp/"
        echo "Available files in /root/:"
        ls -la /root/ | grep -E "(group|vars)" || echo "No group_vars files found in /root/"
        exit 1
    fi
fi

if [[ ! -f "/root/.ssh/config" ]]; then
    echo "ERROR: SSH config file not found at /root/.ssh/config"
    exit 1
fi

# Generate SSH public key from private key if it doesn't exist
if [[ -n "$AWS_SSH_PEM_KEY" ]]; then
    echo "Generating SSH public key from AWS_SSH_PEM_KEY environment variable"

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
    if ssh-keygen -y -f /tmp/ssh_key.pem > /root/.ssh/jenkins-elliptic-validation.pub 2>/dev/null; then
        chmod 644 /root/.ssh/jenkins-elliptic-validation.pub
        echo "SSH public key generated successfully"
        echo "Public key contents:"
        cat /root/.ssh/jenkins-elliptic-validation.pub
    else
        echo "ERROR: Failed to generate public key from SSH private key"
        exit 1
    fi

    # Clean up temporary key file
    rm -f /tmp/ssh_key.pem
else
    echo "WARNING: AWS_SSH_PEM_KEY environment variable is not set"
    exit 1
fi

# Clone or update the qa-infra-automation repository for SSH setup playbook
if [[ ! -d "/root/qa-infra-automation" ]]; then
    echo "Cloning qa-infra-automation repository..."
    git clone -b ${QA_INFRA_REPO_BRANCH:-main} ${QA_INFRA_REPO_URL:-"https://github.com/rancher/qa-infra-automation.git"} /root/qa-infra-automation
else
    echo "Updating qa-infra-automation repository..."
    cd /root/qa-infra-automation
    git fetch origin
    git checkout ${QA_INFRA_REPO_BRANCH:-main}
    git pull origin ${QA_INFRA_REPO_BRANCH:-main}
    cd /root
fi

# Ensure the SSH setup playbook exists
SSH_SETUP_PLAYBOOK="/root/qa-infra-automation/ansible/rke2/airgap/playbooks/setup/setup-ssh-keys.yml"
if [[ ! -f "$SSH_SETUP_PLAYBOOK" ]]; then
    echo "ERROR: SSH setup playbook not found at $SSH_SETUP_PLAYBOOK"
    echo "Available setup playbooks:"
    ls -la "/root/qa-infra-automation/ansible/rke2/airgap/playbooks/setup/" 2>/dev/null || echo "Setup directory not found"
    exit 1
fi

echo "Using SSH setup playbook from qa-infra-automation repository"
echo "Playbook path: $SSH_SETUP_PLAYBOOK"

# Debug: Show inventory file contents
echo "=== Inventory File Contents ==="
cat /root/ansible/rke2/airgap/inventory.yml
echo "=== End Inventory File Contents ==="

# Run the SSH setup playbook from qa-infra-automation
echo "Running SSH setup playbook..."
cd /root/qa-infra-automation/ansible/rke2/airgap
ansible-playbook -i /root/ansible/rke2/airgap/inventory.yml playbooks/setup/setup-ssh-keys.yml -v

echo "SSH setup playbook execution completed"

# Copy playbook execution logs to shared volume
if [[ -f "ansible-playbook.log" ]]; then
    cp ansible-playbook.log /root/ssh_setup_execution.log
fi

echo "=== Ansible SSH Setup Playbook Completed ==="