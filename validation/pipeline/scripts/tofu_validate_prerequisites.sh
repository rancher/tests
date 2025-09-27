#!/bin/bash
set -e

# Consolidated prerequisites validation script for both airgap and destroy operations
# Validates environment, OpenTofu installation, and required files

echo '=== DEBUG: Environment Variables ==='
echo "DEBUG: QA_INFRA_WORK_PATH='${QA_INFRA_WORK_PATH}'"
echo "DEBUG: TF_WORKSPACE='${TF_WORKSPACE}'"
echo "DEBUG: TERRAFORM_VARS_FILENAME='${TERRAFORM_VARS_FILENAME}'"
echo "DEBUG: S3_BUCKET_NAME='${S3_BUCKET_NAME}'"
echo "DEBUG: S3_KEY_PREFIX='${S3_KEY_PREFIX}'"
echo "DEBUG: AWS_REGION='${AWS_REGION}'"
echo '=== END DEBUG ==='

cd ${QA_INFRA_WORK_PATH}

echo 'Checking OpenTofu installation...'
tofu version

echo 'Checking workspace directory...'
test -d ${QA_INFRA_WORK_PATH}

echo 'Validating terraform vars file...'
echo "DEBUG: TERRAFORM_VARS_FILENAME='${TERRAFORM_VARS_FILENAME}'"

echo 'DEBUG: Current working directory:'
pwd
echo 'DEBUG: Contents of current directory:'
ls -la

echo 'DEBUG: Checking if tofu/aws/modules/airgap directory exists:'
if [ -d "${QA_INFRA_WORK_PATH}/tofu/aws/modules/airgap" ]; then
    echo "DEBUG: Directory exists, contents:"
    ls -la "${QA_INFRA_WORK_PATH}/tofu/aws/modules/airgap/"
else
    echo "DEBUG: Directory does not exist: ${QA_INFRA_WORK_PATH}/tofu/aws/modules/airgap"
fi

echo 'DEBUG: Checking if /root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap directory exists:'
if [ -d "/root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap" ]; then
    echo "DEBUG: Directory exists, contents:"
    ls -la "/root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/"
else
    echo "DEBUG: Directory does not exist: /root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap"
fi

# Check both the shared volume location and the qa-infra-automation location
if [ -f "${QA_INFRA_WORK_PATH}/tofu/aws/modules/airgap/${TERRAFORM_VARS_FILENAME}" ]; then
    echo "Found terraform vars file at: ${QA_INFRA_WORK_PATH}/tofu/aws/modules/airgap/${TERRAFORM_VARS_FILENAME}"
elif [ -f "/root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/${TERRAFORM_VARS_FILENAME}" ]; then
    echo "Found terraform vars file at: /root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/${TERRAFORM_VARS_FILENAME}"
else
    echo "ERROR: terraform vars file not found at either location:"
    echo "  1. ${QA_INFRA_WORK_PATH}/tofu/aws/modules/airgap/${TERRAFORM_VARS_FILENAME}"
    echo "  2. /root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/${TERRAFORM_VARS_FILENAME}"
    echo "Available files in tofu/aws/modules/airgap/:"
    ls -la "${QA_INFRA_WORK_PATH}/tofu/aws/modules/airgap/" 2>/dev/null || echo "Directory not accessible"
    ls -la "/root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/" 2>/dev/null || echo "Directory not accessible"
    echo "DEBUG: Testing file existence with explicit paths:"
    test -f "${QA_INFRA_WORK_PATH}/tofu/aws/modules/airgap/cluster.tfvars" && echo "cluster.tfvars found at path 1" || echo "cluster.tfvars NOT found at path 1"
    test -f "/root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/cluster.tfvars" && echo "cluster.tfvars found at path 2" || echo "cluster.tfvars NOT found at path 2"
    exit 1
fi

echo 'All infrastructure prerequisites validated successfully'