#!/bin/bash

set -e
set -o pipefail

# Remove carriage returns from the script
tr -d '\r' < validation/pipeline/scripts/destroy_download_config.sh > /tmp/destroy_download_config.sh
chmod +x /tmp/destroy_download_config.sh

cd "${QA_INFRA_WORK_PATH}"

echo 'Creating configuration directory...'
mkdir -p tofu/aws/modules/airgap

echo 'Downloading cluster.tfvars from S3...'

# Validate S3 parameters
if [ -z "${S3_BUCKET_NAME}" ] || [ -z "${S3_KEY_PREFIX}" ]; then
    echo 'ERROR: S3_BUCKET_NAME and S3_KEY_PREFIX must be set'
    echo "Current values:"
    echo "S3_BUCKET_NAME=${S3_BUCKET_NAME}"
    echo "S3_KEY_PREFIX=${S3_KEY_PREFIX}"
    exit 1
fi

# Extract S3 directory path and filename from KEY_PREFIX
S3_DIR="${S3_KEY_PREFIX%/*}"
S3_FILE="${S3_KEY_PREFIX##*/}"

# Validate extraction
if [ -z "${S3_DIR}" ] || [ -z "${S3_FILE}" ]; then
    echo "ERROR: Invalid S3_KEY_PREFIX format: ${S3_KEY_PREFIX}"
    echo "Expected format: path/to/file.tfvars"
    exit 1
fi

# Download from S3 with explicit path
aws s3 cp \
    "s3://${S3_BUCKET_NAME}/${S3_DIR}" \
    --region "${AWS_REGION}" \
    --recursive \
    --exclude "*" \
    --include "${S3_FILE}" \
    tofu/aws/modules/airgap/

if [ $? -eq 0 ]; then
    echo 'SUCCESS: cluster.tfvars downloaded from S3'
    ls -la "tofu/aws/modules/airgap/${S3_FILE}"
else
    echo 'ERROR: Failed to download cluster.tfvars from S3'
    echo 'Available files in S3 config directory:'
    aws s3 ls "s3://${S3_BUCKET_NAME}/${S3_DIR}" --region "${AWS_REGION}" || echo 'Failed to list S3 contents'
    exit 1
fi

echo 'Configuration files downloaded successfully'
echo 'Verifying downloaded files:'
ls -la tofu/aws/modules/airgap/
