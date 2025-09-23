#!/bin/bash
set -e

cd ${QA_INFRA_WORK_PATH}

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