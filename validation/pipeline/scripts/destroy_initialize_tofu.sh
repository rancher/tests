#!/bin/bash
set -e

echo '=== DEBUG: OpenTofu Initialization ==='
echo "DEBUG: QA_INFRA_WORK_PATH='${QA_INFRA_WORK_PATH}'"
echo "DEBUG: TERRAFORM_BACKEND_VARS_FILENAME='${TERRAFORM_BACKEND_VARS_FILENAME}'"
echo "DEBUG: TF_WORKSPACE='${TF_WORKSPACE}'"

cd ${QA_INFRA_WORK_PATH}

echo 'DEBUG: Current working directory:'
pwd

echo 'DEBUG: Contents of tofu/aws/modules/airgap directory:'
ls -la tofu/aws/modules/airgap/

echo 'DEBUG: Checking if backend.tfvars file exists:'
if [ -f "tofu/aws/modules/airgap/${TERRAFORM_BACKEND_VARS_FILENAME}" ]; then
    echo "DEBUG: backend.tfvars file exists, contents:"
    cat "tofu/aws/modules/airgap/${TERRAFORM_BACKEND_VARS_FILENAME}"
else
    echo "DEBUG: backend.tfvars file does NOT exist"
fi

echo 'DEBUG: Checking if backend.tf file exists:'
if [ -f "tofu/aws/modules/airgap/backend.tf" ]; then
    echo "DEBUG: backend.tf file exists, contents:"
    cat "tofu/aws/modules/airgap/backend.tf"
else
    echo "DEBUG: backend.tf file does NOT exist"
fi

echo 'DEBUG: All .tf and .tfvars files in directory:'
find tofu/aws/modules/airgap/ -name "*.tf" -o -name "*.tfvars" | while read file; do
    echo "=== $file ==="
    cat "$file"
    echo
done

echo '=== END DEBUG ==='

echo 'Initializing OpenTofu with S3 backend configuration...'
tofu -chdir=tofu/aws/modules/airgap init -backend-config="${TERRAFORM_BACKEND_VARS_FILENAME}" -input=false -upgrade

echo 'Verifying initialization success...'
tofu -chdir=tofu/aws/modules/airgap providers

echo 'Checking target workspace: ${TF_WORKSPACE}'

# When TF_WORKSPACE is set, OpenTofu automatically uses it
# We just need to verify the workspace exists or create it if it doesn't
echo 'Checking if workspace exists...'
WORKSPACE_EXISTS=$(tofu -chdir=tofu/aws/modules/airgap workspace list | grep -w "${TF_WORKSPACE}" || true)

if [ -z "$WORKSPACE_EXISTS" ]; then
    echo "Workspace ${TF_WORKSPACE} does not exist, creating it..."
    # Temporarily unset TF_WORKSPACE to allow workspace creation
    unset TF_WORKSPACE
    tofu -chdir=tofu/aws/modules/airgap workspace new "${TF_WORKSPACE}"
    # Set TF_WORKSPACE back for subsequent operations
    export TF_WORKSPACE="${TF_WORKSPACE}"
    echo "Workspace ${TF_WORKSPACE} created successfully"
else
    echo "Workspace ${TF_WORKSPACE} already exists"
fi

echo 'Verifying workspace selection...'
CURRENT_WORKSPACE=$(tofu -chdir=tofu/aws/modules/airgap workspace show)
echo "Current workspace: $CURRENT_WORKSPACE"

if [ "$CURRENT_WORKSPACE" != "${TF_WORKSPACE}" ]; then
    echo "ERROR: Expected workspace ${TF_WORKSPACE}, but got '$CURRENT_WORKSPACE'"
    echo 'Available workspaces:'
    tofu -chdir=tofu/aws/modules/airgap workspace list
    exit 1
fi

echo 'OpenTofu initialization and workspace setup completed successfully'
