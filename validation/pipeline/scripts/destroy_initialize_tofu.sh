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

echo 'Creating/selecting target workspace: ${TF_WORKSPACE}'
tofu -chdir=tofu/aws/modules/airgap workspace select "${TF_WORKSPACE}" || tofu -chdir=tofu/aws/modules/airgap workspace new "${TF_WORKSPACE}"

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