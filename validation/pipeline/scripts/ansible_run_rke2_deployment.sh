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

# Generate group_vars using the centralized script
if [[ ! -f "/root/group_vars/all.yml" ]]; then
    echo "ERROR: /root/group_vars/all.yml not found. Make sure ansible_generate_group_vars.sh was run first."
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
    git clone -b ${QA_INFRA_REPO_BRANCH:-main} ${QA_INFRA_REPO_URL:-"https://github.com/rancher/qa-infra-automation.git"} /root/qa-infra-automation
else
    echo "Updating qa-infra-automation repository..."
    cd /root/qa-infra-automation
    git fetch origin
    git checkout ${QA_INFRA_REPO_BRANCH:-main}
    git pull origin ${QA_INFRA_REPO_BRANCH:-main}
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

# Copy group_vars to the correct location relative to inventory file
# Ansible loads group_vars relative to the inventory file location
mkdir -p /root/ansible/rke2/airgap/group_vars

# Ensure the group_vars file exists and has basic structure
GROUP_VARS_FILE="/root/ansible/rke2/airgap/group_vars/all.yml"
if [[ ! -f "/root/group_vars/all.yml" ]]; then
    echo "ERROR: Source group_vars file not found at /root/group_vars/all.yml"
    exit 1
fi

cp /root/group_vars/all.yml "$GROUP_VARS_FILE"
echo "Copied group_vars to inventory-relative location: $GROUP_VARS_FILE"

# Ensure file ends with newline before any appends
[[ -n $(tail -c1 "$GROUP_VARS_FILE") ]] && echo "" >> "$GROUP_VARS_FILE"

# Ensure RKE2_VERSION is set in the group_vars file
if [[ -n "${RKE2_VERSION}" ]]; then
    echo "Ensuring RKE2_VERSION is set in group_vars: ${RKE2_VERSION}"

    # Check if rke2_version is already in the file
    if grep -q "^rke2_version:" "$GROUP_VARS_FILE"; then
        # Replace existing line
        sed -i "s/^rke2_version:.*/rke2_version: \"${RKE2_VERSION}\"/" "$GROUP_VARS_FILE"
        echo "Updated existing rke2_version in group_vars"
    else
        # Add new line
        echo "rke2_version: \"${RKE2_VERSION}\"" >> "$GROUP_VARS_FILE"
        echo "Added rke2_version to group_vars"
    fi
else
    echo "WARNING: RKE2_VERSION environment variable is not set"
    echo "Setting default RKE2 version"

    # Check if rke2_version is already in the file
    if grep -q "^rke2_version:" "$GROUP_VARS_FILE"; then
        # Replace existing line
        sed -i "s/^rke2_version:.*/rke2_version: \"v1.28.8+rke2r1\"/" "$GROUP_VARS_FILE"
    else
        # Add new line
        echo "rke2_version: \"v1.28.8+rke2r1\"" >> "$GROUP_VARS_FILE"
    fi
fi

# Verify the variable is set
if grep -q "^rke2_version:" "$GROUP_VARS_FILE"; then
    echo "✓ rke2_version successfully set in group_vars:"
    grep "^rke2_version:" "$GROUP_VARS_FILE"
else
    echo "ERROR: Failed to set rke2_version in group_vars file"
    echo "Group_vars file contents:"
    cat "$GROUP_VARS_FILE"
    exit 1
fi

# Display the group_vars file for debugging (with size check to avoid flooding logs)
echo "=== group_vars/all.yml content ==="
TOTAL_LINES=$(wc -l < "$GROUP_VARS_FILE")
FILE_SIZE=$(wc -c < "$GROUP_VARS_FILE")
echo "File size: ${FILE_SIZE} bytes, ${TOTAL_LINES} lines"
echo ""

# If file is reasonable size (<200 lines), show it all; otherwise show head/tail
if [[ ${TOTAL_LINES} -le 200 ]]; then
    echo "--- Full content ---"
    cat "$GROUP_VARS_FILE"
else
    echo "--- First 100 lines ---"
    head -100 "$GROUP_VARS_FILE"
    echo ""
    echo "... (lines $((101)) to $((TOTAL_LINES - 20)) omitted) ..."
    echo ""
    echo "--- Last 20 lines ---"
    tail -20 "$GROUP_VARS_FILE"
fi

echo "=== End group_vars/all.yml ==="

# Validate YAML syntax before running playbook
echo "=== Validating group_vars YAML syntax ==="
if command -v python3 &> /dev/null; then
    if python3 -c "import yaml, sys; yaml.safe_load(open('$GROUP_VARS_FILE'))" 2>&1; then
        echo "✓ group_vars/all.yml YAML is valid"
    else
        echo "✗ group_vars/all.yml has YAML syntax errors (see above)"
        echo "This will cause Ansible to fail. Please fix the YAML syntax in your uploaded file."
        exit 1
    fi
elif command -v yamllint &> /dev/null; then
    if yamllint "$GROUP_VARS_FILE"; then
        echo "✓ group_vars/all.yml YAML is valid"
    else
        echo "✗ group_vars/all.yml has YAML validation errors"
        exit 1
    fi
else
    echo "⚠ No YAML validation tool available (python3 or yamllint)"
    echo "Proceeding without validation - errors may occur during playbook execution"
fi
echo "=== End YAML validation ==="

# Validate Ansible inventory structure
echo "=== Validating Ansible Inventory Structure ==="
INVENTORY_FILE="/root/ansible/rke2/airgap/inventory.yml"

if [[ -f "$INVENTORY_FILE" ]]; then
    echo "✓ Inventory file found at $INVENTORY_FILE"

    # Check for required groups
    echo "Checking inventory structure..."

    if grep -q "rke2_servers:" "$INVENTORY_FILE"; then
        echo "✓ rke2_servers group found"
        SERVER_COUNT=$(grep -A 20 "rke2_servers:" "$INVENTORY_FILE" | grep "rke2-server-" | wc -l)
        echo "  - Server nodes: $SERVER_COUNT"
    else
        echo "✗ rke2_servers group NOT found - this will cause incorrect node roles!"
    fi

    if grep -q "rke2_agents:" "$INVENTORY_FILE"; then
        echo "✓ rke2_agents group found"
        AGENT_COUNT=$(grep -A 20 "rke2_agents:" "$INVENTORY_FILE" | grep "rke2-agent-" | wc -l)
        echo "  - Agent nodes: $AGENT_COUNT"
    else
        echo "✗ rke2_agents group NOT found - this will cause incorrect node roles!"
    fi

    # Check if using old inventory structure (fallback)
    if grep -q "airgap_nodes:" "$INVENTORY_FILE" && ! grep -q "rke2_servers:" "$INVENTORY_FILE"; then
        echo "⚠ WARNING: Using legacy inventory structure (airgap_nodes only)"
        echo "  This may cause all nodes to become control-plane nodes"
        echo "  Consider using the updates/rke2-airgap-improvements branch for proper role separation"
    fi

    # Display inventory structure summary
    echo ""
    echo "=== Inventory Structure Summary ==="
    TOTAL_NODES=$(grep -E "rke2-(server|agent)-[0-9]+" "$INVENTORY_FILE" | wc -l)
    echo "Total RKE2 nodes defined: $TOTAL_NODES"

    if [[ $SERVER_COUNT -gt 0 ]] && [[ $AGENT_COUNT -gt 0 ]]; then
        echo "✓ Proper server/agent role separation detected"
        echo "  Expected cluster structure: 1 control-plane, $((AGENT_COUNT)) worker nodes"
    elif [[ $TOTAL_NODES -gt 1 ]]; then
        echo "⚠ WARNING: Multiple nodes detected but no role separation"
        echo "  This will likely result in all nodes becoming control-plane nodes"
    fi

    # Show inventory content for debugging
    echo ""
    echo "=== Inventory File Content ==="
    cat "$INVENTORY_FILE"
    echo "=== End Inventory Content ==="

else
    echo "✗ ERROR: Inventory file not found at $INVENTORY_FILE"
    exit 1
fi

echo "=== End Inventory Validation ==="

echo "Using RKE2 tarball deployment playbook from qa-infra-automation repository"
echo "Playbook path: $RKE2_TARBALL_PLAYBOOK"

# TEST MODE: Check if we should force failure to test DESTROY_ON_FAILURE
if grep -q "test_force_failure: true" "$GROUP_VARS_FILE" 2>/dev/null; then
    echo "=========================================="
    echo "TEST MODE: Forcing deployment failure to test DESTROY_ON_FAILURE cleanup"
    echo "=========================================="
    echo ""
    echo "To disable this test mode, remove 'test_force_failure: true' from your group_vars/all.yml"
    echo ""
    exit 1
fi

# Run the RKE2 deployment playbook from qa-infra-automation
echo "Running RKE2 tarball deployment playbook..."
cd /root/qa-infra-automation/ansible/rke2/airgap

# Pass RKE2_VERSION as an extra variable to ensure it's available during pre_tasks
EXTRA_VARS=""
if [[ -n "${RKE2_VERSION}" ]]; then
    EXTRA_VARS="-e rke2_version=${RKE2_VERSION}"
    echo "Passing RKE2_VERSION as extra variable: ${RKE2_VERSION}"
fi

# Run ansible-playbook and capture exit code
ansible-playbook -i /root/ansible/rke2/airgap/inventory.yml playbooks/deploy/rke2-tarball-playbook.yml -v ${EXTRA_VARS}
ANSIBLE_EXIT_CODE=$?

# Check if Ansible failed critically (exit code 2 is for failed tasks, but deployment may still succeed)
if [[ $ANSIBLE_EXIT_CODE -eq 2 ]]; then
    echo "WARNING: Ansible playbook had failed tasks (exit code 2), but checking if deployment succeeded..."
    # Check if the cluster is actually working by testing kubectl connectivity
    if [[ -f /root/.kube/config ]]; then
        export KUBECONFIG=/root/.kube/config
        if kubectl get nodes --no-headers | grep -q "Ready"; then
            echo "SUCCESS: Despite Ansible task failures, cluster is operational with ready nodes"
            echo "Treating this as successful deployment"
            ANSIBLE_EXIT_CODE=0
        else
            echo "ERROR: Ansible failed and cluster is not operational"
        fi
    else
        echo "ERROR: Ansible failed and no kubeconfig found"
    fi
elif [[ $ANSIBLE_EXIT_CODE -ne 0 ]]; then
    echo "ERROR: Ansible playbook failed with exit code $ANSIBLE_EXIT_CODE"
fi

echo "RKE2 tarball deployment playbook execution completed"

# Copy playbook execution logs to shared volume
if [[ -f "ansible-playbook.log" ]]; then
    cp ansible-playbook.log /root/rke2_tarball_deployment_execution.log
fi

# Verify node roles after deployment (only if deployment succeeded)
if [[ $ANSIBLE_EXIT_CODE -eq 0 ]]; then
    echo "=== Verifying RKE2 Node Roles ==="

    # Run the node role verification playbook if it exists
    NODE_ROLE_PLAYBOOK="/root/qa-infra-automation/ansible/rke2/airgap/playbooks/debug/check-node-roles.yml"
    if [[ -f "$NODE_ROLE_PLAYBOOK" ]]; then
        echo "Running node role verification playbook..."
        cd /root/qa-infra-automation/ansible/rke2/airgap
        ansible-playbook -i /root/ansible/rke2/airgap/inventory.yml playbooks/debug/check-node-roles.yml -v

        if [[ $? -eq 0 ]]; then
            echo "✓ Node role verification completed"
        else
            echo "⚠ Node role verification had issues"
        fi
    else
        echo "Node role verification playbook not found, performing manual check..."

        # Manual node role check
        cd /root/qa-infra-automation/ansible/rke2/airgap
        if ansible-playbook -i /root/ansible/rke2/airgap/inventory.yml -c local -m shell -a "export KUBECONFIG=/etc/rancher/rke2/rke2.yaml && /var/lib/rancher/rke2/bin/kubectl get nodes -o wide 2>/dev/null || echo 'kubectl not available'" localhost 2>/dev/null; then
            echo "✓ Node roles checked manually"
        else
            echo "⚠ Could not verify node roles manually"
        fi
    fi

    echo "=== End Node Role Verification ==="
fi

# Copy kubeconfig to shared volume for Jenkins archival if deployment succeeded
if [[ $ANSIBLE_EXIT_CODE -eq 0 ]]; then
    echo "Copying kubeconfig to shared volume for archival..."
    KUBECONFIG_LOCATIONS=(
        "/root/.kube/config"
        "/etc/rancher/rke2/rke2.yaml"
        "/root/ansible/rke2/airgap/kubeconfig"
    )

    KUBECONFIG_FOUND=false
    for config_path in "${KUBECONFIG_LOCATIONS[@]}"; do
        if [[ -f "$config_path" ]]; then
            echo "Found kubeconfig at: $config_path"
            cp "$config_path" /root/kubeconfig.yaml
            chmod 644 /root/kubeconfig.yaml
            echo "✓ Kubeconfig copied to /root/kubeconfig.yaml for archival"
            KUBECONFIG_FOUND=true
            break
        fi
    done

    if [[ "$KUBECONFIG_FOUND" == false ]]; then
        echo "⚠ WARNING: Kubeconfig not found after successful RKE2 deployment"
    fi
fi

echo "=== Ansible RKE2 Tarball Deployment Completed ==="

# Exit with the determined exit code
exit $ANSIBLE_EXIT_CODE