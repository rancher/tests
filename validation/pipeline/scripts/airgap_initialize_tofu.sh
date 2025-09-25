#!/bin/bash
set -e

cd ${QA_INFRA_WORK_PATH}

echo 'Initializing OpenTofu with S3 backend configuration...'
tofu -chdir=tofu/aws/modules/airgap init -backend-config="${TERRAFORM_BACKEND_VARS_FILENAME}" -input=false -upgrade

echo 'Verifying initialization success...'
tofu -chdir=tofu/aws/modules/airgap providers

echo 'OpenTofu initialization completed successfully'