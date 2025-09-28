#!/bin/bash
set -e

# Ansible Group Vars Generation Script
# This script generates Ansible group_vars/all.yml from ANSIBLE_CONFIG parameter

echo "=== Ansible Group Vars Generation Started ==="

# Validate required environment variables
if [[ -z "${ANSIBLE_VARIABLES}" ]]; then
    echo "ERROR: ANSIBLE_VARIABLES environment variable is not set"
    exit 1
fi

if [[ -z "${QA_INFRA_WORK_PATH}" ]]; then
    echo "ERROR: QA_INFRA_WORK_PATH environment variable is not set"
    exit 1
fi

# Create group_vars directory structure
mkdir -p /root/group_vars

echo "Creating group_vars/all.yml from ANSIBLE_VARIABLES parameter"

# Write the ANSIBLE_VARIABLES content to group_vars/all.yml
cat > /root/group_vars/all.yml << EOF
# Ansible Group Variables Generated from ANSIBLE_VARIABLES Parameter
# Generated on: $(date)
# Workspace: ${TF_WORKSPACE}

${ANSIBLE_VARIABLES}

# Additional system-generated variables
ansible_python_interpreter: /usr/bin/python3
ansible_ssh_pipelining: true
ansible_ssh_retries: 3
ansible_ssh_timeout: 30

# RKE2 specific variables
rke2_install_method: tarball
rke2_airgap_mode: true
rke2_download_dir: /tmp/rke2-airgap
rke2_images_dir: /tmp/rke2-images
rke2_config_dir: /etc/rancher/rke2
rke2_data_dir: /var/lib/rancher/rke2

# Kubernetes configuration
kube_config_dir: /etc/rancher/k3s
kubelet_config_dir: /var/lib/kubelet

# Airgap deployment specific settings
airgap_bundle_download_url: ""
airgap_bundle_checksum_url: ""
airgap_bundle_local_path: "/tmp/rke2-airgap-bundle.tar.gz"
airgap_images_local_path: "/tmp/rke2-images.tar.gz"

# Private registry configuration (if provided)
EOF

# Add private registry configuration if URL is provided
if [[ -n "${PRIVATE_REGISTRY_URL}" ]]; then
    cat >> /root/group_vars/all.yml << EOF
private_registry:
  enabled: true
  url: "${PRIVATE_REGISTRY_URL}"
  username: "${PRIVATE_REGISTRY_USERNAME}"
  password: "${PRIVATE_REGISTRY_PASSWORD}"
  skip_tls_verify: true

# RKE2 registry configuration
rke2_registry:
  config:
    mirrors:
      docker.io:
        endpoint:
          - "${PRIVATE_REGISTRY_URL}"
      registry.k8s.io:
        endpoint:
          - "${PRIVATE_REGISTRY_URL}"
      gcr.io:
        endpoint:
          - "${PRIVATE_REGISTRY_URL}"
      quay.io:
        endpoint:
          - "${PRIVATE_REGISTRY_URL}"
EOF
else
    cat >> /root/group_vars/all.yml << EOF
private_registry:
  enabled: false

# RKE2 registry configuration (no private registry)
rke2_registry:
  config: {}
EOF
fi

# Add version-specific configurations
cat >> /root/group_vars/all.yml << EOF

# Version-specific configurations
rke2_version: "${RKE2_VERSION}"
rancher_version: "${RANCHER_VERSION}"

# Network configuration
rke2_network:
  pod_cidr: "10.42.0.0/16"
  service_cidr: "10.43.0.0/16"
  cluster_dns: "10.43.0.10"
  cluster_domain: "cluster.local"

# RKE2 server configuration
rke2_server_config:
  disable: ["rke2-ingress-nginx"]
  tls-san:
    - "${BASTION_IP}"
    - "${BASTION_PRIVATE_IP}"
  etcd-s3: false
  cloud-provider-name: "aws"

# RKE2 agent configuration
rke2_agent_config:
  node-name: "{{ inventory_hostname }}"
  with-node-id: true

# System configuration
system_config:
  packages:
    - containerd
    - conntrack
    - socat
    - ebtables
    - ipset
    - iptables
    - curl
    - wget
    - jq
    - unzip
    - tar
  
  services:
    - containerd
    - rke2-server
    - rke2-agent

# Firewall configuration
firewall_config:
  enabled: true
  ports:
    - "6443/tcp"  # Kubernetes API server
    - "8472/udp"  # Flannel VXLAN
    - "10250/tcp" # Kubelet API
    - "2379/tcp"  # etcd client
    - "2380/tcp"  # etcd peer
    - "9345/tcp"  # RKE2 supervisor
    - "6443/tcp"  # RKE2 supervisor

# Logging configuration
logging_config:
  enabled: true
  level: "info"
  format: "json"

# Health check configuration
health_check:
  enabled: true
  timeout: 300
  retries: 3
  delay: 10
EOF

echo "Ansible group_vars/all.yml generated successfully:"
echo "Group vars file location: /root/group_vars/all.yml"

# Display group_vars for verification (excluding sensitive data)
echo "=== Generated Group Vars (sanitized) ==="
grep -v "password\|secret\|key" /root/group_vars/all.yml | head -50
echo "=== End Group Vars (sanitized) ==="

# Copy group_vars to shared volume for persistence
cp -r /root/group_vars /root/group_vars.backup

echo "=== Ansible Group Vars Generation Completed ==="