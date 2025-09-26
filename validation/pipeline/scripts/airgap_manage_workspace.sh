#!/bin/bash
set -e

# Export AWS credentials for OpenTofu
export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY}"
export AWS_REGION="${AWS_REGION:-us-east-2}"
export AWS_DEFAULT_REGION="${AWS_REGION:-us-east-2}"

cd ${QA_INFRA_WORK_PATH}
export TF_WORKSPACE="${TF_WORKSPACE}"

echo 'Managing workspace state...'

echo 'Current workspaces:'
tofu -chdir=tofu/aws/modules/airgap workspace list

echo "Creating or selecting workspace: ${TF_WORKSPACE}"

# Store the workspace name for operations
WORKSPACE_NAME="${TF_WORKSPACE}"

# Check if workspace exists and create if needed
# When TF_WORKSPACE is set, we cannot manually select workspaces, so we only create if needed
if ! tofu -chdir=tofu/aws/modules/airgap workspace list | grep -q "^[[:space:]]*${WORKSPACE_NAME}[[:space:]]*$"; then
    echo 'Workspace does not exist, creating new workspace...'
    # Temporarily unset TF_WORKSPACE to allow workspace creation
    unset TF_WORKSPACE
    tofu -chdir=tofu/aws/modules/airgap workspace new "${WORKSPACE_NAME}"
    echo "Workspace '${WORKSPACE_NAME}' created successfully"
    # Re-export TF_WORKSPACE - this automatically activates the workspace for all subsequent operations
    export TF_WORKSPACE="${WORKSPACE_NAME}"
else
    echo "Workspace '${WORKSPACE_NAME}' already exists and will be used automatically"
fi

# Final verification that workspace exists
echo "Final workspace verification..."
if tofu -chdir=tofu/aws/modules/airgap workspace list | grep -q "${WORKSPACE_NAME}"; then
    echo "✓ Workspace '${WORKSPACE_NAME}' confirmed to exist"
    echo "✓ TF_WORKSPACE environment variable: ${TF_WORKSPACE}"
else
    echo "ERROR: Workspace '${WORKSPACE_NAME}' not found"
    echo 'Available workspaces:'
    tofu -chdir=tofu/aws/modules/airgap workspace list
    exit 1
fi

echo "Workspace management completed successfully for: ${WORKSPACE_NAME}"

# Re-initialize to ensure workspace is properly configured
tofu -chdir=tofu/aws/modules/airgap init -input=false -upgrade