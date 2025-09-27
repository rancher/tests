#!/bin/bash
set -e

# Consolidated OpenTofu initialization script for both airgap and destroy operations
# Supports both environment file sourcing and direct environment variable passing

echo '=== DEBUG: OpenTofu Initialization ==='
echo "DEBUG: QA_INFRA_WORK_PATH='${QA_INFRA_WORK_PATH}'"
echo "DEBUG: TERRAFORM_BACKEND_VARS_FILENAME='${TERRAFORM_BACKEND_VARS_FILENAME}'"
echo "DEBUG: TF_WORKSPACE='${TF_WORKSPACE}'"

# Source environment file if it exists (airgap compatibility)
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
    echo "Environment file not found at /tmp/.env, using Docker environment variables"
    # Fallback to environment variables passed by Docker (destroy compatibility)
    export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID}"
    export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY}"
    export AWS_REGION="${AWS_REGION:-us-east-2}"
    export AWS_DEFAULT_REGION="${AWS_REGION:-us-east-2}"
fi

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

# Check if backend.tf exists and use appropriate initialization method
if [ -f "tofu/aws/modules/airgap/backend.tf" ]; then
    echo "Using backend.tf configuration"
    tofu -chdir=tofu/aws/modules/airgap init -input=false -upgrade
elif [ -f "tofu/aws/modules/airgap/${TERRAFORM_BACKEND_VARS_FILENAME}" ]; then
    echo "Using backend.tfvars configuration"
    tofu -chdir=tofu/aws/modules/airgap init -backend-config="${TERRAFORM_BACKEND_VARS_FILENAME}" -input=false -upgrade
else
    echo "ERROR: Neither backend.tf nor backend.tfvars found"
    exit 1
fi

echo 'Verifying initialization success...'
tofu -chdir=tofu/aws/modules/airgap providers

echo 'OpenTofu initialization completed successfully'