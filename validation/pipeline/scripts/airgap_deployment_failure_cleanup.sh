#!/bin/bash
set -e

cd ${QA_INFRA_WORK_PATH}
export TF_WORKSPACE="${TF_WORKSPACE}"

echo 'Performing infrastructure cleanup due to deployment failure...'

tofu -chdir=tofu/aws/modules/airgap init -backend-config="${TERRAFORM_BACKEND_VARS_FILENAME}" -input=false -upgrade || echo 'Init failed during cleanup - manual intervention required'

tofu -chdir=tofu/aws/modules/airgap destroy -auto-approve -var-file="${TERRAFORM_VARS_FILENAME}" || echo 'Destroy command failed - manual cleanup may be required'
echo 'Cleanup attempt completed'