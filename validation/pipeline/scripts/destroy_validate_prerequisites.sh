#!/bin/bash
set -e

cd ${QA_INFRA_WORK_PATH}

echo 'Checking OpenTofu installation...'
tofu version

echo 'Checking workspace directory...'
test -d ${QA_INFRA_WORK_PATH}

echo 'Validating terraform vars file...'
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
    exit 1
fi

echo 'All infrastructure prerequisites validated successfully'