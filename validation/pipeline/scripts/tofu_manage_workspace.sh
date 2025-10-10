#!/bin/bash
set -e

# Consolidated OpenTofu workspace management script for both airgap and destroy operations
# Handles workspace creation, selection, and verification

echo '=== DEBUG: Workspace Management ==='
echo "DEBUG: QA_INFRA_WORK_PATH='${QA_INFRA_WORK_PATH}'"
echo "DEBUG: TF_WORKSPACE='${TF_WORKSPACE}'"

# Export AWS credentials for OpenTofu (airgap compatibility)
export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY}"
export AWS_REGION="${AWS_REGION:-us-east-2}"
export AWS_DEFAULT_REGION="${AWS_REGION:-us-east-2}"

cd ${QA_INFRA_WORK_PATH}

echo 'Managing workspace state...'
echo 'Current workspaces:'
tofu -chdir=tofu/aws/modules/airgap workspace list

echo "Target workspace: ${TF_WORKSPACE}"

# Store the workspace name for operations
WORKSPACE_NAME="${TF_WORKSPACE}"

# Check if workspace exists and create if needed
# When TF_WORKSPACE is set, OpenTofu automatically uses it
# We just need to verify the workspace exists or create it if it doesn't
echo 'Checking if workspace exists...'
WORKSPACE_EXISTS=$(tofu -chdir=tofu/aws/modules/airgap workspace list | grep -w "${WORKSPACE_NAME}" || true)

if [ -z "$WORKSPACE_EXISTS" ]; then
    echo "Workspace ${WORKSPACE_NAME} does not exist, creating it..."
    # Temporarily unset TF_WORKSPACE to allow workspace creation
    unset TF_WORKSPACE
    tofu -chdir=tofu/aws/modules/airgap workspace new "${WORKSPACE_NAME}"
    # Set TF_WORKSPACE back for subsequent operations
    export TF_WORKSPACE="${WORKSPACE_NAME}"
    echo "Workspace ${WORKSPACE_NAME} created successfully"
else
    echo "Workspace ${WORKSPACE_NAME} already exists"
fi

echo 'Verifying workspace selection...'
CURRENT_WORKSPACE=$(tofu -chdir=tofu/aws/modules/airgap workspace show)
echo "Current workspace: $CURRENT_WORKSPACE"

if [ "$CURRENT_WORKSPACE" != "${WORKSPACE_NAME}" ]; then
    echo "ERROR: Expected workspace ${WORKSPACE_NAME}, but got '$CURRENT_WORKSPACE'"
    echo 'Available workspaces:'
    tofu -chdir=tofu/aws/modules/airgap workspace list
    exit 1
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

# Re-initialize to ensure workspace is properly configured (airgap compatibility)
echo 'Re-initializing to ensure workspace is properly configured...'
tofu -chdir=tofu/aws/modules/airgap init -input=false -upgrade

echo '=== END DEBUG ==='