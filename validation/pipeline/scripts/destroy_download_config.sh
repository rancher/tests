#!/bin/bash

set -e
set -o pipefail

cd "${QA_INFRA_WORK_PATH}"

echo 'Creating configuration directory...'
mkdir -p tofu/aws/modules/airgap

echo 'Downloading cluster.tfvars from S3...'

# Validate S3 parameters
echo "DEBUG: Checking S3 parameters..."
echo "DEBUG: S3_BUCKET_NAME='${S3_BUCKET_NAME}'"
echo "DEBUG: S3_KEY_PREFIX='${S3_KEY_PREFIX}'"
echo "DEBUG: AWS_REGION='${AWS_REGION}'"

if [ -z "${S3_BUCKET_NAME}" ]; then
    echo 'ERROR: S3_BUCKET_NAME must be set'
    echo 'This parameter should be provided in the Jenkins job configuration'
    echo 'Default value: jenkins-terraform-state-storage'
    echo 'Please check the Jenkins job parameters or environment variables'
    exit 1
fi

if [ -z "${S3_KEY_PREFIX}" ]; then
    echo 'ERROR: S3_KEY_PREFIX must be set'
    echo 'This parameter should be provided in the Jenkins job configuration'
    echo 'Default value: jenkins-airgap-rke2/terraform.tfstate'
    echo 'Please check the Jenkins job parameters or environment variables'
    exit 1
fi

if [ -z "${AWS_REGION}" ]; then
    echo 'ERROR: AWS_REGION must be set'
    echo 'This parameter should be provided in the Jenkins job configuration'
    echo 'Default value: us-east-2'
    echo 'Please check the Jenkins job parameters or environment variables'
    exit 1
fi

echo "DEBUG: All S3 parameters are valid"

echo "Using S3 configuration:"
echo "  Bucket: ${S3_BUCKET_NAME}"
echo "  Key Prefix: ${S3_KEY_PREFIX}"
echo "  Region: ${AWS_REGION}"

# Extract S3 directory path and filename from KEY_PREFIX
S3_DIR="${S3_KEY_PREFIX%/*}"
S3_FILE="${S3_KEY_PREFIX##*/}"

# Validate extraction
if [ -z "${S3_DIR}" ] || [ -z "${S3_FILE}" ]; then
    echo "ERROR: Invalid S3_KEY_PREFIX format: ${S3_KEY_PREFIX}"
    echo "Expected format: path/to/file.tfvars"
    echo "Example: jenkins-airgap-rke2/terraform.tfstate"
    exit 1
fi

echo "Parsed S3 path:"
echo "  Directory: ${S3_DIR}"
echo "  File: ${S3_FILE}"

# Download from S3 with explicit path
echo "Downloading s3://${S3_BUCKET_NAME}/${S3_DIR}/${S3_FILE}..."
aws s3 cp \
    "s3://${S3_BUCKET_NAME}/${S3_DIR}/${S3_FILE}" \
    "tofu/aws/modules/airgap/${S3_FILE}" \
    --region "${AWS_REGION}"

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