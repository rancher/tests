#!/bin/bash
set -e

# Source environment file to load variables
if [ -f /tmp/.env ]; then
    echo "Sourcing environment file: /tmp/.env"
    source /tmp/.env
else
    echo "WARNING: Environment file not found at /tmp/.env"
fi

# Export AWS credentials for OpenTofu
export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY}"
export AWS_REGION="${AWS_REGION:-us-east-2}"
export AWS_DEFAULT_REGION="${AWS_REGION:-us-east-2}"

cd ${QA_INFRA_WORK_PATH}
export TF_WORKSPACE="${TF_WORKSPACE}"

echo 'Debug: Environment variables for S3 operations...'
echo "S3_BUCKET_NAME=${S3_BUCKET_NAME}"
echo "S3_REGION=${S3_REGION}"
echo "TF_WORKSPACE=${TF_WORKSPACE}"
echo "TERRAFORM_VARS_FILENAME=${TERRAFORM_VARS_FILENAME}"
echo "TERRAFORM_BACKEND_VARS_FILENAME=${TERRAFORM_BACKEND_VARS_FILENAME}"

# Validate required S3 variables
if [ -z "${S3_BUCKET_NAME}" ]; then
    echo 'ERROR: S3_BUCKET_NAME environment variable is not set or empty'
    exit 1
fi

if [ -z "${S3_REGION}" ]; then
    echo 'ERROR: S3_REGION environment variable is not set or empty'
    exit 1
fi

echo 'S3 environment variables validated successfully'

echo 'Debug: Listing current directory contents...'
ls -la .

echo 'Debug: Listing mounted qa-infra-automation/tofu/aws/modules/airgap contents...'
ls -la /root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/ || echo 'Mounted directory listing failed'

echo 'Restoring plan file from shared volume...'
if [ -f /root/tfplan-backup ]; then
    # Restore plan file to the correct module directory
    cp /root/tfplan-backup /root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/tfplan
    echo 'Plan file restored from shared volume to module directory'
else
    echo 'WARNING: No backup plan file found in shared volume, generating new plan...'
    tofu -chdir=/root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap plan -input=false -var-file="${TERRAFORM_VARS_FILENAME}" -out=tfplan
    if [ $? -ne 0 ]; then
        echo 'ERROR: Plan generation failed'
        exit 1
    fi
fi

# Check if plan was restored/generated successfully in module directory
if [ ! -f /root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/tfplan ]; then
    echo 'ERROR: Plan file was not generated successfully in module directory'
    ls -la /root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/ || echo 'Cannot list module directory'
    exit 1
fi

# Verify the plan file is not empty
PLAN_SIZE=$(stat -c%s /root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/tfplan 2>/dev/null || echo 0)
if [ "$PLAN_SIZE" = "0" ]; then
    echo 'ERROR: Plan file is empty'
    exit 1
fi

echo 'Plan file restored successfully ($PLAN_SIZE bytes), applying...'
echo 'Starting tofu apply...'
tofu -chdir=/root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap apply -auto-approve -input=false tfplan
APPLY_RC=$?

echo "Tofu apply completed with return code: $APPLY_RC"
if [ $APPLY_RC -ne 0 ]; then
    echo 'ERROR: Tofu apply failed'
    exit 1
fi

# Clean up the plan file after successful application
rm -f /root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/tfplan

echo 'Debug: Listing module directory after apply...'
ls -la /root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/

echo 'Verifying state after apply...'
tofu -chdir=/root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap state list

echo 'Backing up terraform state immediately after apply...'
# Handle both local and remote state backends
STATE_FILE="/root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/terraform.tfstate"
BACKUP_TIMESTAMP=$(date +%Y%m%d-%H%M%S)

# Check if local state file exists (local backend)
if [ -f "$STATE_FILE" ]; then
    echo 'Local state file found, creating backups...'
    cp "$STATE_FILE" "$STATE_FILE.backup-$BACKUP_TIMESTAMP"
    cp "$STATE_FILE" /root/terraform-state-primary.tfstate
    cp "$STATE_FILE" /root/terraform.tfstate
    STATE_SIZE=$(stat -c%s "$STATE_FILE" 2>/dev/null || echo 0)
    echo "SUCCESS: Local terraform.tfstate backed up successfully ($STATE_SIZE bytes)"
    ls -la "$STATE_FILE"
else
    echo 'Local state file not found, assuming remote backend. Pulling state...'
    cd /root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap

    # Pull the current state from remote backend
    if tofu state pull > /tmp/terraform.tfstate.tmp 2>/dev/null; then
        # Verify the pulled state is not empty
        if [ -s /tmp/terraform.tfstate.tmp ]; then
            # Create backups with the pulled state
            cp /tmp/terraform.tfstate.tmp "$STATE_FILE.backup-$BACKUP_TIMESTAMP"
            cp /tmp/terraform.tfstate.tmp /root/terraform-state-primary.tfstate
            cp /tmp/terraform.tfstate.tmp /root/terraform.tfstate
            STATE_SIZE=$(stat -c%s /tmp/terraform.tfstate.tmp 2>/dev/null || echo 0)
            echo "SUCCESS: Remote terraform state pulled and backed up successfully ($STATE_SIZE bytes)"

            # Clean up temporary file
            rm -f /tmp/terraform.tfstate.tmp
        else
            echo 'ERROR: Pulled state file is empty'
            rm -f /tmp/terraform.tfstate.tmp
            exit 1
        fi
    else
        echo 'ERROR: Failed to pull terraform state from remote backend'
        exit 1
    fi
fi

echo 'Backing up terraform variables file for archival...'
cp /root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/"${TERRAFORM_VARS_FILENAME}" /root/"${TERRAFORM_VARS_FILENAME}"

echo 'Uploading configuration files to S3 bucket...'
# Check if S3_BUCKET_NAME is set before attempting uploads
if [ -z "${S3_BUCKET_NAME}" ]; then
    echo 'WARNING: S3_BUCKET_NAME is not set, skipping S3 uploads'
    echo 'Configuration files will remain local only'
else
    # Upload cluster.tfvars to S3
    echo 'Uploading cluster.tfvars to S3...'
    S3_TARGET="s3://${S3_BUCKET_NAME}/env:/${TF_WORKSPACE}/config/${TERRAFORM_VARS_FILENAME}"
    echo "S3 target URL: ${S3_TARGET}"

    if aws s3 cp /root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/"${TERRAFORM_VARS_FILENAME}" \
        "${S3_TARGET}" \
        --region "${S3_REGION}"; then
        echo 'SUCCESS: cluster.tfvars uploaded to S3'
    else
        echo 'WARNING: Failed to upload cluster.tfvars to S3'
    fi

    # Upload backend.tfvars to S3
    echo 'Uploading backend.tfvars to S3...'
    S3_TARGET="s3://${S3_BUCKET_NAME}/env:/${TF_WORKSPACE}/config/${TERRAFORM_BACKEND_VARS_FILENAME}"
    echo "S3 target URL: ${S3_TARGET}"

    if aws s3 cp /root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap/"${TERRAFORM_BACKEND_VARS_FILENAME}" \
        "${S3_TARGET}" \
        --region "${S3_REGION}"; then
        echo 'SUCCESS: backend.tfvars uploaded to S3'
    else
        echo 'WARNING: Failed to upload backend.tfvars to S3'
    fi
fi

echo 'Configuration files upload completed'

echo 'Generating outputs for downstream stages...'
tofu -chdir=/root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap output -json > /root/infrastructure-outputs.json

echo 'Verifying inventory file generation...'
if [ -f /root/go/src/github.com/rancher/qa-infra-automation/ansible/rke2/airgap/inventory/inventory.yml ] && [ -s /root/go/src/github.com/rancher/qa-infra-automation/ansible/rke2/airgap/inventory/inventory.yml ]; then
    echo 'SUCCESS: inventory.yml generated by tofu apply exists and has content'
    cp /root/go/src/github.com/rancher/qa-infra-automation/ansible/rke2/airgap/inventory/inventory.yml /root/ansible-inventory.yml
else
    echo 'WARNING: inventory.yml not found or empty after apply'
fi

echo 'Infrastructure apply completed successfully'