#!/bin/bash
set -e

# Ansible Failure Cleanup Script
# This script handles cleanup when Ansible deployment fails

echo "=== Ansible Failure Cleanup Started ==="

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
    echo "WARNING: terraform.tfstate not found - cannot perform cleanup"
    exit 0
fi

# Create cleanup log file
cat > /root/ansible-failure-cleanup.log << EOF
# Ansible Failure Cleanup Log
# Generated on: $(date)
# Workspace: ${TF_WORKSPACE}
# Reason: Ansible deployment failure

Cleanup operations performed:
EOF

# Function to log cleanup operations
log_cleanup() {
    echo "[$(date)] $1" >> /root/ansible-failure-cleanup.log
    echo "$1"
}

# Attempt to gather information about the current state
log_cleanup "Gathering current infrastructure state..."

# Get list of all resources
log_cleanup "Extracting resource list from Terraform state..."
terraform state list > /root/terraform-state-list.txt 2>/dev/null || log_cleanup "Failed to list Terraform state resources"

# Get outputs for debugging
log_cleanup "Extracting Terraform outputs..."
terraform output -json > /root/terraform-outputs.json 2>/dev/null || log_cleanup "Failed to extract Terraform outputs"

# Create failure summary
cat > /root/ansible-failure-summary.txt << EOF
# Ansible Deployment Failure Summary
# Generated on: $(date)
# Workspace: ${TF_WORKSPACE}

Failure Details:
- Timestamp: $(date)
- Workspace: ${TF_WORKSPACE}
- Reason: Ansible deployment failure
- Terraform state: Available

Infrastructure Status:
EOF

# Add infrastructure status information
if [[ -f "/root/terraform-state-list.txt" ]]; then
    echo "- Terraform resources: $(wc -l < /root/terraform-state-list.txt)" >> /root/ansible-failure-summary.txt
fi

if [[ -f "/root/terraform-outputs.json" ]]; then
    echo "- Terraform outputs: Available" >> /root/ansible-failure-summary.txt
fi

# Add resource counts by type
log_cleanup "Analyzing resource types..."
if [[ -f "/root/terraform-state-list.txt" ]]; then
    echo "" >> /root/ansible-failure-summary.txt
    echo "Resource Types:" >> /root/ansible-failure-summary.txt
    grep -o '^[^.]*\.' /root/terraform-state-list.txt | sort | uniq -c | sort -nr >> /root/ansible-failure-summary.txt
fi

# Attempt to destroy infrastructure if DESTROY_ON_FAILURE is enabled
if [[ "${DESTROY_ON_FAILURE}" == "true" ]]; then
    log_cleanup "DESTROY_ON_FAILURE is enabled - attempting infrastructure destruction..."
    
    # Create backup of current state before destruction
    log_cleanup "Creating backup of current Terraform state..."
    cp terraform.tfstate "/root/terraform-state-before-destroy-$(date +%Y%m%d-%H%M%S).tfstate"
    
    # Attempt to destroy infrastructure
    log_cleanup "Running terraform destroy..."
    if terraform destroy -auto-approve -no-color > /root/terraform-destroy.log 2>&1; then
        log_cleanup "✓ Infrastructure destroyed successfully"
        echo "- Infrastructure status: Destroyed" >> /root/ansible-failure-summary.txt
    else
        log_cleanup "✗ Infrastructure destruction failed"
        echo "- Infrastructure status: Destruction failed" >> /root/ansible-failure-summary.txt
        echo "- Destruction log: /root/terraform-destroy.log" >> /root/ansible-failure-summary.txt
    fi
else
    log_cleanup "DESTROY_ON_FAILURE is disabled - preserving infrastructure"
    echo "- Infrastructure status: Preserved (manual cleanup required)" >> /root/ansible-failure-summary.txt
fi

# Create cleanup artifacts archive
log_cleanup "Creating cleanup artifacts archive..."
tar -czf /root/ansible-failure-artifacts.tar.gz \
    /root/ansible-failure-cleanup.log \
    /root/ansible-failure-summary.txt \
    /root/terraform-state-list.txt \
    /root/terraform-outputs.json \
    /root/terraform-destroy.log \
    /root/terraform-state-before-destroy-*.tfstate \
    2>/dev/null || log_cleanup "Failed to create cleanup artifacts archive"

# Generate final cleanup report
cat > /root/ansible-cleanup-report.txt << EOF
# Ansible Failure Cleanup Report
# Generated on: $(date)
# Workspace: ${TF_WORKSPACE}

Cleanup Summary:
- Start time: $(date)
- End time: $(date)
- Workspace: ${TF_WORKSPACE}
- Cleanup triggered by: Ansible deployment failure
- DESTROY_ON_FAILURE: ${DESTROY_ON_FAILURE}

Actions Taken:
1. Gathered infrastructure state information
2. Created backup of Terraform state
3. Extracted resource lists and outputs
4. Generated failure summary
5. ${DESTROY_ON_FAILURE:-"Attempted to destroy infrastructure" || "Preserved infrastructure"}

Artifacts Generated:
- /root/ansible-failure-cleanup.log
- /root/ansible-failure-summary.txt
- /root/terraform-state-list.txt
- /root/terraform-outputs.json
- /root/terraform-destroy.log (if destruction attempted)
- /root/terraform-state-before-destroy-*.tfstate (if destruction attempted)
- /root/ansible-failure-artifacts.tar.gz

Next Steps:
EOF

if [[ "${DESTROY_ON_FAILURE}" == "true" ]]; then
    if [[ -f "/root/terraform-destroy.log" ]] && grep -q "Destroy complete!" /root/terraform-destroy.log; then
        echo "- Infrastructure has been successfully destroyed" >> /root/ansible-cleanup-report.txt
    else
        echo "- Infrastructure destruction may have failed - manual verification required" >> /root/ansible-cleanup-report.txt
    fi
else
    echo "- Infrastructure has been preserved" >> /root/ansible-cleanup-report.txt
    echo "- Manual cleanup required for workspace: ${TF_WORKSPACE}" >> /root/ansible-cleanup-report.txt
    echo "- Run destroy pipeline or manually clean up resources" >> /root/ansible-cleanup-report.txt
fi

log_cleanup "Cleanup process completed"
echo "Cleanup report generated: /root/ansible-cleanup-report.txt"

# Copy cleanup artifacts to shared volume
cp /root/ansible-failure-cleanup.log /root/ansible-failure-cleanup.log.backup
cp /root/ansible-failure-summary.txt /root/ansible-failure-summary.txt.backup
cp /root/ansible-cleanup-report.txt /root/ansible-cleanup-report.txt.backup

echo "=== Ansible Failure Cleanup Completed ==="