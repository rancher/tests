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

# Write the ANSIBLE_VARIABLES content to a temporary file
# Use printf to avoid bash variable expansion
printf '%s' "${ANSIBLE_VARIABLES}" | tr -d '\r' > /tmp/group_vars/all.yml.template

# Debug: Show what we're starting with
echo "=== Template file before substitution (first 50 lines) ==="
head -50 /tmp/group_vars/all.yml.template
echo "=== End template preview ==="

# Perform variable substitution
# Note: We expect ${VAR} format in the template (not \${VAR})
cp /tmp/group_vars/all.yml.template /tmp/group_vars/all.yml.working

# Function to safely replace a variable in the file
replace_var() {
    local var_name="$1"
    local var_value="$2"
    local temp_file="/tmp/group_vars/all.yml.tmp"

    # Escape special characters in the value for use in sed
    # Escape: / & \ and newlines
    local escaped_value=$(printf '%s\n' "$var_value" | sed -e 's/[\/&]/\\&/g' | sed ':a;N;$!ba;s/\n/\\n/g')

    # Replace ${VAR_NAME} with value
    sed "s|\${${var_name}}|${escaped_value}|g" /tmp/group_vars/all.yml.working > "$temp_file"
    mv "$temp_file" /tmp/group_vars/all.yml.working
}

# Perform substitutions in order
echo "Substituting variables..."
echo "  RKE2_VERSION=${RKE2_VERSION}"
replace_var "RKE2_VERSION" "${RKE2_VERSION}"

echo "  RANCHER_VERSION=${RANCHER_VERSION}"
replace_var "RANCHER_VERSION" "${RANCHER_VERSION}"

echo "  HOSTNAME_PREFIX=${HOSTNAME_PREFIX}"
replace_var "HOSTNAME_PREFIX" "${HOSTNAME_PREFIX}"

if [[ -n "${PRIVATE_REGISTRY_URL}" ]]; then
    echo "  PRIVATE_REGISTRY_URL=${PRIVATE_REGISTRY_URL}"
    replace_var "PRIVATE_REGISTRY_URL" "${PRIVATE_REGISTRY_URL}"
fi

if [[ -n "${PRIVATE_REGISTRY_USERNAME}" ]]; then
    echo "  PRIVATE_REGISTRY_USERNAME=${PRIVATE_REGISTRY_USERNAME}"
    replace_var "PRIVATE_REGISTRY_USERNAME" "${PRIVATE_REGISTRY_USERNAME}"
fi

if [[ -n "${PRIVATE_REGISTRY_PASSWORD}" ]]; then
    echo "  PRIVATE_REGISTRY_PASSWORD=***"
    replace_var "PRIVATE_REGISTRY_PASSWORD" "${PRIVATE_REGISTRY_PASSWORD}"
fi

# Move final result
mv /tmp/group_vars/all.yml.working /tmp/group_vars/all.yml

# Clean up
rm -f /tmp/group_vars/all.yml.template

echo "Ansible group_vars/all.yml created successfully from user-provided content"
echo "Group vars file location: /tmp/group_vars/all.yml"

# Display group_vars for verification (excluding sensitive data)
echo "=== Generated Group Vars (sanitized) ==="
grep -v "password\|secret\|key" /tmp/group_vars/all.yml | head -100
echo "=== End Group Vars (sanitized) ==="

# Validate YAML syntax
echo "=== Validating YAML syntax ==="
if command -v python3 &> /dev/null; then
    python3 -c "import yaml, sys; yaml.safe_load(open('/tmp/group_vars/all.yml'))" 2>&1 && echo "✓ YAML is valid" || echo "✗ YAML has syntax errors (see above)"
elif command -v yamllint &> /dev/null; then
    yamllint /tmp/group_vars/all.yml || echo "✗ YAML has validation errors"
else
    echo "⚠ No YAML validation tool available (python3 or yamllint)"
fi
echo "=== End YAML validation ==="

# Copy group_vars to shared volume for persistence
cp -r /tmp/group_vars /tmp/group_vars.backup

# Also copy group_vars to /root/ for Ansible playbook compatibility
mkdir -p /root/group_vars
cp /tmp/group_vars/all.yml /root/group_vars/all.yml
echo "Group_vars file also copied to /root/group_vars/all.yml for Ansible compatibility"

echo "=== Ansible Group Vars Generation Completed ==="