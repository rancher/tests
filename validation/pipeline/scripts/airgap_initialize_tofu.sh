#!/bin/bash
set -e

# Export AWS credentials for OpenTofu
export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY}"
export AWS_REGION="${AWS_REGION:-us-east-2}"
export AWS_DEFAULT_REGION="${AWS_REGION:-us-east-2}"

cd ${QA_INFRA_WORK_PATH}

# Debug: Show backend configuration file content
if [ -f "tofu/aws/modules/airgap/backend.tf" ]; then
    echo "=== DEBUG: Backend.tf file exists ==="
    cat "tofu/aws/modules/airgap/backend.tf"
    echo "=== END DEBUG ==="
else
    echo "ERROR: Backend configuration file not found: tofu/aws/modules/airgap/backend.tf"
    exit 1
fi

echo 'Initializing OpenTofu with S3 backend configuration...'
# Simple init since backend.tf now contains the proper S3 configuration
tofu -chdir=tofu/aws/modules/airgap init -input=false -upgrade

echo 'Verifying initialization success...'
tofu -chdir=tofu/aws/modules/airgap providers

echo 'OpenTofu initialization completed successfully'