#!/bin/bash
set -e

# Ansible Group Variables Generation Script
# This script generates the Ansible group_vars/all.yml file from the ANSIBLE_VARIABLES environment variable
# Uses the airgap library for the actual generation

# Load the airgap library
source "/root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap_lib.sh"

echo "=== Ansible Group Variables Generation Started ==="
echo "Timestamp: $(date)"
echo "Working directory: $(pwd)"

# Validate required environment variables
validate_required_vars "ANSIBLE_VARIABLES"

# Set default output directory
OUTPUT_DIR="${ANSIBLE_OUTPUT_DIR:-/root/ansible/rke2/airgap/group_vars}"

echo "Configuration:"
echo "  Output directory: $OUTPUT_DIR"
echo "  ANSIBLE_VARIABLES size: ${#ANSIBLE_VARIABLES} bytes"

# Generate the group_vars using the airgap library function
if generate_group_vars "$OUTPUT_DIR"; then
    echo "=== Group Variables Generation Summary ==="
    echo "Generated files:"
    if [[ -f "$OUTPUT_DIR/all.yml" ]]; then
        echo "  - $OUTPUT_DIR/all.yml ($(wc -l < "$OUTPUT_DIR/all.yml") lines)"
    fi

    # Show first few lines of generated content
    if [[ -f "$OUTPUT_DIR/all.yml" ]]; then
        echo ""
        echo "=== Generated Content Preview ==="
        head -10 "$OUTPUT_DIR/all.yml"
        echo "=== End Preview ==="
    fi

    echo "=== Group Variables Generation Completed Successfully ==="
else
    echo "ERROR: Failed to generate group variables"
    exit 1
fi