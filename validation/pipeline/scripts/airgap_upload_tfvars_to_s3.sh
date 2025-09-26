#!/bin/bash
set -e

# Source environment file to load variables
if [ -f /tmp/.env ]; then
    echo "Sourcing environment file for S3 upload: /tmp/.env"
    source /tmp/.env

    # Debug: Show relevant environment variables
    echo "=== DEBUG: S3 Upload Environment Variables ==="
    echo "S3_BUCKET_NAME=${S3_BUCKET_NAME}"
    echo "S3_REGION=${S3_REGION}"
    echo "S3_KEY_PREFIX=${S3_KEY_PREFIX}"
    echo "TF_WORKSPACE=${TF_WORKSPACE}"
    echo "TERRAFORM_VARS_FILENAME=${TERRAFORM_VARS_FILENAME}"
    echo "AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID:+[SET]}"
    echo "AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY:+[SET]}"
    echo "AWS_REGION=${AWS_REGION}"
    echo "=== END DEBUG ==="

    # Export the sourced variables explicitly
    export S3_BUCKET_NAME="${S3_BUCKET_NAME}"
    export S3_REGION="${S3_REGION}"
    export S3_KEY_PREFIX="${S3_KEY_PREFIX}"
    export TF_WORKSPACE="${TF_WORKSPACE}"
    export TERRAFORM_VARS_FILENAME="${TERRAFORM_VARS_FILENAME}"
else
    echo "WARNING: Environment file not found at /tmp/.env"
fi

# Export AWS credentials for AWS CLI
export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY}"
export AWS_REGION="${AWS_REGION:-${S3_REGION}}"
export AWS_DEFAULT_REGION="${AWS_REGION:-${S3_REGION}}"

# Navigate to the qa-infra-automation working directory
cd ${QA_INFRA_WORK_PATH}

# Validate required S3 variables with fallback values
if [ -z "${S3_BUCKET_NAME}" ]; then
    echo 'WARNING: S3_BUCKET_NAME is empty, using fallback value'
    export S3_BUCKET_NAME="jenkins-terraform-state-storage"
fi

if [ -z "${S3_REGION}" ]; then
    echo 'WARNING: S3_REGION is empty, using fallback value'
    export S3_REGION="us-east-2"
fi

if [ -z "${S3_KEY_PREFIX}" ]; then
    echo 'WARNING: S3_KEY_PREFIX is empty, using fallback value'
    export S3_KEY_PREFIX="jenkins-airgap-rke2"
fi

if [ -z "${TF_WORKSPACE}" ]; then
    echo 'ERROR: TF_WORKSPACE is required for S3 upload'
    exit 1
fi

if [ -z "${TERRAFORM_VARS_FILENAME}" ]; then
    echo 'WARNING: TERRAFORM_VARS_FILENAME is empty, using fallback value'
    export TERRAFORM_VARS_FILENAME="cluster.tfvars"
fi

# Construct S3 target path
S3_TARGET="s3://${S3_BUCKET_NAME}/env:/${TF_WORKSPACE}/config/${TERRAFORM_VARS_FILENAME}"
LOCAL_FILE_PATH="tofu/aws/modules/airgap/${TERRAFORM_VARS_FILENAME}"

echo "Uploading cluster.tfvars to S3..."
echo "Local file: ${LOCAL_FILE_PATH}"
echo "S3 target: ${S3_TARGET}"
echo "S3 region: ${S3_REGION}"

# Check if local file exists
if [ ! -f "${LOCAL_FILE_PATH}" ]; then
    echo "ERROR: Local cluster.tfvars file not found at: ${LOCAL_FILE_PATH}"
    echo "Contents of tofu/aws/modules/airgap/:"
    ls -la "tofu/aws/modules/airgap/" || echo "Directory not found"
    exit 1
fi

# Upload to S3 using AWS CLI
if aws s3 cp "${LOCAL_FILE_PATH}" "${S3_TARGET}" --region "${S3_REGION}" --no-cli-pager; then
    echo "✅ SUCCESS: cluster.tfvars uploaded to S3: ${S3_TARGET}"
else
    echo "⚠️ WARNING: Failed to upload cluster.tfvars to S3: ${S3_TARGET}"
    echo "S3 upload failed, but script will exit successfully to avoid breaking deployment"
    # Exit successfully to avoid breaking the entire deployment pipeline
fi

echo "S3 upload operation completed"