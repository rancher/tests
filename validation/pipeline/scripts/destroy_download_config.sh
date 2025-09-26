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
aws s3 cp \
    "s3://${S3_BUCKET_NAME}/${S3_KEY_PREFIX}" \
    --region "${AWS_REGION}" \
    --recursive \
    --exclude "*" \
    --include "cluster.tfvars"

if [ $? -eq 0 ]; then
    echo 'SUCCESS: cluster.tfvars downloaded from S3'
    ls -la "tofu/aws/modules/airgap/${TERRAFORM_VARS_FILENAME}"
else
    echo 'ERROR: Failed to download cluster.tfvars from S3'
    echo 'Available files in S3 config directory:'
    aws s3 ls "s3://${S3_BUCKET_NAME}/env:${TF_WORKSPACE}/config/" --region "${AWS_REGION}" || echo 'Failed to list S3 contents'
    exit 1
fi

echo 'Configuration files downloaded successfully'
echo 'Verifying downloaded files:'
ls -la tofu/aws/modules/airgap/