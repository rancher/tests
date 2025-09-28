#!/bin/bash
set -e

# Ansible Inventory Generation Script
# This script generates Ansible inventory from Terraform outputs

echo "=== Ansible Inventory Generation Started ==="

# Validate required environment variables
if [[ -z "${TF_WORKSPACE}" ]]; then
    echo "ERROR: TF_WORKSPACE environment variable is not set"
    exit 1
fi

if [[ -z "${QA_INFRA_WORK_PATH}" ]]; then
    echo "ERROR: QA_INFRA_WORK_PATH environment variable is not set"
    exit 1
fi

# Change to the Terraform directory
cd "${QA_INFRA_WORK_PATH}/tofu/aws/modules/airgap"

echo "Working directory: $(pwd)"

# Check if terraform.tfstate exists
if [[ ! -f "terraform.tfstate" ]]; then
    echo "ERROR: terraform.tfstate not found in current directory"
    echo "Contents of current directory:"
    ls -la
    exit 1
fi

# Extract Terraform outputs
echo "Extracting Terraform outputs..."

# Get bastion host information
BASTION_IP=$(terraform output -json bastion_public_ip | jq -r '.value')
BASTION_PRIVATE_IP=$(terraform output -json bastion_private_ip | jq -r '.value')

# Get RKE2 server nodes
RKE2_SERVERS=$(terraform output -json rke2_server_private_ips | jq -r '.value[]')

# Get RKE2 agent nodes
RKE2_AGENTS=$(terraform output -json rke2_agent_private_ips | jq -r '.value[]')

# Validate extracted values
if [[ -z "${BASTION_IP}" ]] || [[ "${BASTION_IP}" == "null" ]]; then
    echo "ERROR: Failed to extract bastion IP from Terraform outputs"
    exit 1
fi

echo "Extracted values:"
echo "  Bastion IP: ${BASTION_IP}"
echo "  Bastion Private IP: ${BASTION_PRIVATE_IP}"
echo "  RKE2 Servers: ${RKE2_SERVERS}"
echo "  RKE2 Agents: ${RKE2_AGENTS}"

# Create Ansible inventory file
cat > /root/ansible-inventory.yml << EOF
# Ansible Inventory Generated from Terraform Outputs
# Generated on: $(date)
# Workspace: ${TF_WORKSPACE}

all:
  hosts:
    bastion:
      ansible_host: ${BASTION_IP}
      ansible_private_ip: ${BASTION_PRIVATE_IP}
      ansible_user: ubuntu
      ansible_ssh_common_args: '-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null'
      ansible_become: true
      ansible_become_method: sudo
  children:
    rke2_servers:
      hosts:
EOF

# Add RKE2 server nodes to inventory
for server_ip in ${RKE2_SERVERS}; do
    cat >> /root/ansible-inventory.yml << EOF
        rke2-server-$(echo ${server_ip} | tr '.' '-'):
          ansible_host: ${server_ip}
          ansible_user: ubuntu
          ansible_ssh_common_args: '-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ProxyCommand="ssh -W %h:%p ubuntu@${BASTION_IP}"'
          ansible_become: true
          ansible_become_method: sudo
EOF
done

# Add RKE2 agents section if agents exist
if [[ -n "${RKE2_AGENTS}" ]]; then
    cat >> /root/ansible-inventory.yml << EOF
    rke2_agents:
      hosts:
EOF

    for agent_ip in ${RKE2_AGENTS}; do
        cat >> /root/ansible-inventory.yml << EOF
        rke2-agent-$(echo ${agent_ip} | tr '.' '-'):
          ansible_host: ${agent_ip}
          ansible_user: ubuntu
          ansible_ssh_common_args: '-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ProxyCommand="ssh -W %h:%p ubuntu@${BASTION_IP}"'
          ansible_become: true
          ansible_become_method: sudo
EOF
    done
fi

# Add groups for easier targeting
cat >> /root/ansible-inventory.yml << EOF
    kubernetes_nodes:
      children:
        rke2_servers:
        rke2_agents:
    
    rke2_cluster:
      children:
        rke2_servers:
        rke2_agents:
    
    airgap_nodes:
      children:
        rke2_servers:
        rke2_agents:
EOF

echo "Ansible inventory generated successfully:"
echo "Inventory file location: /root/ansible-inventory.yml"

# Display inventory for verification
echo "=== Generated Inventory ==="
cat /root/ansible-inventory.yml
echo "=== End Inventory ==="

# Copy inventory to shared volume for persistence
cp /root/ansible-inventory.yml /root/ansible-inventory.yml.backup

echo "=== Ansible Inventory Generation Completed ==="