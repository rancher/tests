#!/bin/bash
set -e

cd ${QA_INFRA_WORK_PATH}

echo 'Creating configuration directory...'
# cspell:ignore airgap
mkdir -p tofu/aws/modules/airgap

# cspell:ignore tfvars
echo 'Downloading cluster.tfvars from S3...'
aws s3 cp s3://${S3_BUCKET_NAME}/env:/${TF_WORKSPACE}/config/${TERRAFORM_VARS_FILENAME} ${QA_INFRA_WORK_PATH}/tofu/aws/modules/airgap/${TERRAFORM_VARS_FILENAME} --region ${S3_REGION}

if [ $? -eq 0 ]; then
    echo 'SUCCESS: cluster.tfvars downloaded from S3'
    ls -la tofu/aws/modules/airgap/${TERRAFORM_VARS_FILENAME}
else
    echo 'ERROR: Failed to download cluster.tfvars from S3'
    echo 'Available files in S3 config directory:'
    aws s3 ls s3://${S3_BUCKET_NAME}/env:/${TF_WORKSPACE}/config/ --region ${S3_REGION} || echo 'Failed to list S3 contents'
    exit 1
fi
echo 'Configuration files downloaded successfully'
echo 'Verifying downloaded files:'
ls -la ${QA_INFRA_WORK_PATH}/tofu/aws/modules/airgap/*.tfvars