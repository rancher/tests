#!/bin/bash
set -e

# Export AWS credentials for OpenTofu
export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY}"
export AWS_REGION="${AWS_REGION:-us-east-2}"
export AWS_DEFAULT_REGION="${AWS_REGION:-us-east-2}"

cd ${QA_INFRA_WORK_PATH}
export TF_WORKSPACE="${TF_WORKSPACE}"

echo 'Generating infrastructure plan for validation...'
tofu -chdir=tofu/aws/modules/airgap plan -input=false -var-file="${TERRAFORM_VARS_FILENAME}" -out=tfplan

echo 'Checking if plan file was generated in the correct location...'
if [ ! -f tofu/aws/modules/airgap/tfplan ]; then
    echo 'ERROR: Plan file was not generated successfully in module directory'
    echo 'Contents of tofu/aws/modules/airgap/:'
    ls -la tofu/aws/modules/airgap/
    exit 1
fi

echo 'Verifying plan file is not empty...'
PLAN_SIZE=$(stat -c%s tofu/aws/modules/airgap/tfplan 2>/dev/null || echo 0)
if [ "$PLAN_SIZE" = "0" ]; then
    echo 'ERROR: Plan file is empty'
    exit 1
fi

echo "Plan file generated successfully ($PLAN_SIZE bytes) in tofu/aws/modules/airgap/tfplan"

# Copy plan file from module directory to shared volume for persistence
cp tofu/aws/modules/airgap/tfplan /root/tfplan-backup
echo 'Plan file backed up to shared volume'

echo 'Infrastructure plan validation completed'