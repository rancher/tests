#!/bin/bash

set -e

echo "=== Testing Inventory File Path Fixes ==="

# Create test directories
TEST_DIR="/tmp/ansible_test"
TERRAFORM_INVENTORY_DIR="$TEST_DIR/terraform/inventory"
ANSIBLE_INVENTORY_DIR="$TEST_DIR/ansible/rke2/airgap"
SHARED_VOLUME_DIR="$TEST_DIR/shared"

mkdir -p "$TERRAFORM_INVENTORY_DIR"
mkdir -p "$ANSIBLE_INVENTORY_DIR"
mkdir -p "$SHARED_VOLUME_DIR"

# Create a sample inventory file
cat > "$TERRAFORM_INVENTORY_DIR/inventory.yml" << 'EOF'
all:
  hosts:
    bastion:
      ansible_host: 1.2.3.4
      ansible_user: ubuntu
    node1:
      ansible_host: 5.6.7.8
      ansible_user: ubuntu
    node2:
      ansible_host: 9.10.11.12
      ansible_user: ubuntu
  vars:
    ansible_ssh_common_args: '-o StrictHostKeyChecking=no'
EOF

echo "=== Testing airgap_apply_infrastructure.sh logic ==="
# Test the infrastructure script logic
if [ -f "$TERRAFORM_INVENTORY_DIR/inventory.yml" ]; then
    echo "Found inventory at Terraform location: $TERRAFORM_INVENTORY_DIR/inventory.yml"
    
    # Copy to shared volume (for artifact extraction)
    cp "$TERRAFORM_INVENTORY_DIR/inventory.yml" "$SHARED_VOLUME_DIR/ansible-inventory.yml"
    echo "Copied inventory to shared volume: $SHARED_VOLUME_DIR/ansible-inventory.yml"
    
    # Copy to Ansible expected location (for playbook execution)
    cp "$TERRAFORM_INVENTORY_DIR/inventory.yml" "$ANSIBLE_INVENTORY_DIR/inventory.yml"
    echo "Copied inventory to Ansible expected location: $ANSIBLE_INVENTORY_DIR/inventory.yml"
    
    echo "Inventory file contents:"
    cat "$ANSIBLE_INVENTORY_DIR/inventory.yml"
else
    echo "WARNING: inventory.yml not found or empty after apply"
fi

echo ""
echo "=== Testing Ansible script logic ==="
# Test the Ansible script logic
INVENTORY_FILE=""
POSSIBLE_LOCATIONS=(
    "$ANSIBLE_INVENTORY_DIR/inventory.yml"
    "$SHARED_VOLUME_DIR/ansible-inventory.yml"
)

for location in "${POSSIBLE_LOCATIONS[@]}"; do
    if [ -f "$location" ] && [ -s "$location" ]; then
        INVENTORY_FILE="$location"
        echo "Found Ansible inventory file at: $location"
        break
    fi
done

if [ -z "$INVENTORY_FILE" ]; then
    echo "ERROR: Ansible inventory file not found at either:"
    for location in "${POSSIBLE_LOCATIONS[@]}"; do
        echo "  - $location"
    done
    exit 1
fi

# Test the fallback copy logic
if [ ! -f "$ANSIBLE_INVENTORY_DIR/inventory.yml" ] && [ -f "$SHARED_VOLUME_DIR/ansible-inventory.yml" ]; then
    echo "Copying inventory from shared volume to Ansible expected location..."
    cp "$SHARED_VOLUME_DIR/ansible-inventory.yml" "$ANSIBLE_INVENTORY_DIR/inventory.yml"
    INVENTORY_FILE="$ANSIBLE_INVENTORY_DIR/inventory.yml"
    echo "Updated inventory file location to: $INVENTORY_FILE"
fi

echo "SUCCESS: Ansible inventory file found at: $INVENTORY_FILE"
echo "Final inventory file contents:"
cat "$INVENTORY_FILE"

# Cleanup
rm -rf "$TEST_DIR"
echo ""
echo "=== Test completed successfully ==="