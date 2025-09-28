#!/bin/bash
set -e

# Ansible SSH Key Setup Script
# This script sets up SSH keys for Ansible connectivity

echo "=== Ansible SSH Key Setup Started ==="

# Validate required environment variables
if [[ -z "${AWS_SSH_PEM_KEY}" ]]; then
    echo "ERROR: AWS_SSH_PEM_KEY environment variable is not set"
    exit 1
fi

if [[ -z "${AWS_SSH_KEY_NAME}" ]]; then
    echo "ERROR: AWS_SSH_KEY_NAME environment variable is not set"
    exit 1
fi

# Create .ssh directory
mkdir -p /root/.ssh
chmod 700 /root/.ssh

# Decode and write SSH private key
echo "${AWS_SSH_PEM_KEY}" | base64 -d > /root/.ssh/${AWS_SSH_KEY_NAME}
chmod 600 /root/.ssh/${AWS_SSH_KEY_NAME}

# Create SSH config file
cat > /root/.ssh/config << EOF
# SSH Configuration for Ansible
# Generated on: $(date)
# Workspace: ${TF_WORKSPACE}

Host *
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null
    ServerAliveInterval 60
    ServerAliveCountMax 3
    TCPKeepAlive yes
    Compression yes
    ControlMaster auto
    ControlPath ~/.ssh/master-%r@%h:%p
    ControlPersist 600

Host bastion
    HostName ${BASTION_IP}
    User ubuntu
    IdentityFile ~/.ssh/${AWS_SSH_KEY_NAME}
    ForwardAgent yes

Host rke2-server-*
    User ubuntu
    IdentityFile ~/.ssh/${AWS_SSH_KEY_NAME}
    ProxyCommand ssh -W %h:%p ubuntu@${BASTION_IP}
    ForwardAgent yes

Host rke2-agent-*
    User ubuntu
    IdentityFile ~/.ssh/${AWS_SSH_KEY_NAME}
    ProxyCommand ssh -W %h:%p ubuntu@${BASTION_IP}
    ForwardAgent yes
EOF

chmod 644 /root/.ssh/config

echo "SSH key setup completed:"
echo "  SSH private key: /root/.ssh/${AWS_SSH_KEY_NAME}"
echo "  SSH config: /root/.ssh/config"

# Test SSH connectivity to bastion
echo "Testing SSH connectivity to bastion..."
if ssh -o BatchMode=yes -o ConnectTimeout=10 bastion 'echo "SSH connection successful"'; then
    echo "✓ SSH connectivity to bastion verified"
else
    echo "✗ SSH connectivity to bastion failed"
    echo "Checking SSH configuration..."
    ls -la /root/.ssh/
    echo "SSH config contents:"
    cat /root/.ssh/config
    exit 1
fi

# Copy SSH keys to shared volume for persistence
cp -r /root/.ssh /root/.ssh.backup

echo "=== Ansible SSH Key Setup Completed ==="