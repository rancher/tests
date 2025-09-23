#!/bin/bash
set -e

cd ${QA_INFRA_WORK_PATH}

echo 'Checking OpenTofu installation...'
tofu version

echo 'Checking workspace directory...'
test -d ${QA_INFRA_WORK_PATH}

echo 'Validating terraform vars file...'
test -f ${QA_INFRA_WORK_PATH}/tofu/aws/modules/airgap/${TERRAFORM_VARS_FILENAME}

echo 'All infrastructure prerequisites validated successfully'