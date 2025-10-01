#!/bin/bash
set -e

# Ansible Group Vars Generation Script
# This script uses the ANSIBLE_VARIABLES parameter content directly as group_vars/all.yml

echo "=== Ansible Group Vars Generation Started ==="

# Validate required environment variables
if [[ -z "${QA_INFRA_WORK_PATH}" ]]; then
    echo "ERROR: QA_INFRA_WORK_PATH environment variable is not set"
    exit 1
fi

# Handle empty ANSIBLE_VARIABLES - use default configuration
if [[ -z "${ANSIBLE_VARIABLES}" ]]; then
    echo "WARNING: ANSIBLE_VARIABLES environment variable is not set or empty"
    echo "Creating minimal default Ansible configuration"
    ANSIBLE_VARIABLES=$(cat << 'YAML_EOF'
---
# Default Ansible configuration
ansible_python_interpreter: /usr/bin/python3
rke2_version: "${RKE2_VERSION}"
rancher_version: "${RANCHER_VERSION}"
hostname_prefix: "${HOSTNAME_PREFIX}"
rancher_hostname: "${HOSTNAME_PREFIX}.qa.rancher.space"
YAML_EOF
)
fi

# Create group_vars directory structure
mkdir -p /tmp/group_vars

echo "Creating group_vars/all.yml from ANSIBLE_VARIABLES parameter"
echo "Using user-provided configuration directly"

# Write the ANSIBLE_VARIABLES content and expand environment variables
# Use envsubst to replace ${VAR} patterns with actual environment variable values
echo "${ANSIBLE_VARIABLES}" | envsubst > /tmp/group_vars/all.yml

echo "Ansible group_vars/all.yml created successfully from user-provided content"
echo "Group vars file location: /tmp/group_vars/all.yml"

# Display group_vars for verification (excluding sensitive data)
echo "=== Generated Group Vars (sanitized) ==="
grep -v "password\|secret\|key" /tmp/group_vars/all.yml | head -50
echo "=== End Group Vars (sanitized) ==="

# Copy group_vars to shared volume for persistence
cp -r /tmp/group_vars /tmp/group_vars.backup

# Also copy group_vars to /root/ for Ansible playbook compatibility
mkdir -p /root/group_vars
cp /tmp/group_vars/all.yml /root/group_vars/all.yml
echo "Group_vars file also copied to /root/group_vars/all.yml for Ansible compatibility"

echo "=== Ansible Group Vars Generation Completed ==="