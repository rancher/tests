#!/bin/bash
set -e

cd ${QA_INFRA_WORK_PATH}
export TF_WORKSPACE="${TF_WORKSPACE}"

echo 'Debug: Listing current directory contents...'
ls -la .

echo 'Debug: Listing mounted qa-infra-automation/tofu/aws/modules/airgap contents...'
ls -la /root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/ || echo 'Mounted directory listing failed'

echo "--- Starting Direct Destruction ---"
# Pass placeholder variables directly to the destroy command to satisfy required variables
tofu -chdir=/root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap destroy -auto-approve -input=false -var-file="${TERRAFORM_VARS_FILENAME}"

echo "--- Verifying Destruction ---"
REMAINING_RESOURCES=$(tofu -chdir=/root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap state list | wc -l || echo 0)
if [ "$REMAINING_RESOURCES" -eq 0 ]; then
    echo "✅ Success: All resources have been destroyed."
    # Clean up the workspace
    echo "--- Cleaning up Terraform Workspace ---"
    unset TF_WORKSPACE
    tofu -chdir=/root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap workspace select default || echo "Could not switch to default workspace."
    tofu -chdir=/root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap workspace delete "${TARGET_WORKSPACE}" || echo "Could not delete workspace ${TARGET_WORKSPACE}."
else
    echo "❌ WARNING: $REMAINING_RESOURCES resources still remain in state."
    tofu -chdir=/root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap state list
    exit 1
fi