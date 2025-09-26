#!/bin/bash
set -e

# Export AWS credentials for OpenTofu
export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY}"
export AWS_REGION="${AWS_REGION:-us-east-2}"
export AWS_DEFAULT_REGION="${AWS_REGION:-us-east-2}"

cd ${QA_INFRA_WORK_PATH}

echo 'Initializing OpenTofu with S3 backend configuration...'
tofu -chdir=tofu/aws/modules/airgap init -backend=s3 -backend-config="${TERRAFORM_BACKEND_VARS_FILENAME}" -input=false -upgrade

echo 'Verifying initialization success...'
tofu -chdir=tofu/aws/modules/airgap providers

echo 'OpenTofu initialization completed successfully'