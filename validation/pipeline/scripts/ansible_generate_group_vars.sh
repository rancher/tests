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
# First, ensure it has Unix line endings (remove any CR characters)
echo "${ANSIBLE_VARIABLES}" | tr -d '\r' > /tmp/group_vars/all.yml.template

# Use a more robust substitution method with proper escaping
# Create a temporary script to do the substitutions safely
cat > /tmp/substitute_vars.sh << 'SUBST_EOF'
#!/bin/bash
# Read the template file
INPUT_FILE="$1"
OUTPUT_FILE="$2"

# Function to escape special characters for sed
escape_for_sed() {
    printf '%s\n' "$1" | sed -e 's/[\/&]/\\&/g'
}

# Escape all values
RKE2_VERSION_ESC=$(escape_for_sed "${RKE2_VERSION}")
RANCHER_VERSION_ESC=$(escape_for_sed "${RANCHER_VERSION}")
HOSTNAME_PREFIX_ESC=$(escape_for_sed "${HOSTNAME_PREFIX}")
PRIVATE_REGISTRY_URL_ESC=$(escape_for_sed "${PRIVATE_REGISTRY_URL}")
PRIVATE_REGISTRY_USERNAME_ESC=$(escape_for_sed "${PRIVATE_REGISTRY_USERNAME}")
PRIVATE_REGISTRY_PASSWORD_ESC=$(escape_for_sed "${PRIVATE_REGISTRY_PASSWORD}")

# Perform substitutions
sed \
  -e "s|\\\${RKE2_VERSION}|${RKE2_VERSION_ESC}|g" \
  -e "s|\\\${RANCHER_VERSION}|${RANCHER_VERSION_ESC}|g" \
  -e "s|\\\${HOSTNAME_PREFIX}|${HOSTNAME_PREFIX_ESC}|g" \
  -e "s|\\\${PRIVATE_REGISTRY_URL}|${PRIVATE_REGISTRY_URL_ESC}|g" \
  -e "s|\\\${PRIVATE_REGISTRY_USERNAME}|${PRIVATE_REGISTRY_USERNAME_ESC}|g" \
  -e "s|\\\${PRIVATE_REGISTRY_PASSWORD}|${PRIVATE_REGISTRY_PASSWORD_ESC}|g" \
  "${INPUT_FILE}" > "${OUTPUT_FILE}"
SUBST_EOF

chmod +x /tmp/substitute_vars.sh
/tmp/substitute_vars.sh /tmp/group_vars/all.yml.template /tmp/group_vars/all.yml

# Clean up
rm -f /tmp/group_vars/all.yml.template /tmp/substitute_vars.sh

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