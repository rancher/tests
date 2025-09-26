#!/bin/bash
set -e

# Source environment file to load variables
if [ -f /tmp/.env ]; then
    echo "Sourcing environment file: /tmp/.env"
    source /tmp/.env

    # Debug: Check if AWS variables are set after sourcing
    echo "=== DEBUG: AWS Variables after sourcing ==="
    echo "AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID:+[SET]}"
    echo "AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY:+[SET]}"
    echo "AWS_REGION=${AWS_REGION}"
    echo "S3_BUCKET_NAME=${S3_BUCKET_NAME}"
    echo "S3_REGION=${S3_REGION}"
    echo "S3_KEY_PREFIX=${S3_KEY_PREFIX}"
    echo "=== END DEBUG ==="

    # Export the sourced variables explicitly to ensure they're available
    export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID}"
    export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY}"
    export AWS_REGION="${AWS_REGION:-us-east-2}"
    export AWS_DEFAULT_REGION="${AWS_REGION:-us-east-2}"
    export S3_BUCKET_NAME="${S3_BUCKET_NAME}"
    export S3_REGION="${S3_REGION}"
    export S3_KEY_PREFIX="${S3_KEY_PREFIX}"
else
    echo "WARNING: Environment file not found at /tmp/.env"
    # Fallback to environment variables passed by Docker
    export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID}"
    export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY}"
    export AWS_REGION="${AWS_REGION:-us-east-2}"
    export AWS_DEFAULT_REGION="${AWS_REGION:-us-east-2}"
fi

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