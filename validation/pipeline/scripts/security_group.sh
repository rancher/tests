#!/bin/bash
set -ex

echo "Authorize security group access for this runner"

: "${QAINFRA_SCRIPT_PATH:=/root/go/src/github.com/rancher/qa-infra-automation}"
: "${SG_MODULE_PATH:=tofu/aws/modules/sg}"
: "${SG_TFVARS_FILE:=terraform.tfvars}"
: "${SECURITY_GROUP_NAME:?SECURITY_GROUP_NAME is required}"
: "${VPC_ID:?VPC_ID is required}"
: "${RUNNER_PUBLIC_IP:?RUNNER_PUBLIC_IP is required}"
: "${SG_DESCRIPTION:=jenkins-runner}"
: "${AWS_DEFAULT_REGION:=us-east-2}"
: "${ACTION:=apply}"

cd "$QAINFRA_SCRIPT_PATH/$SG_MODULE_PATH"

cat > "$SG_TFVARS_FILE" <<EOF
vpc_id            = "$VPC_ID"
security_group_name = "$SECURITY_GROUP_NAME"
allowed_cidrs     = ["$RUNNER_PUBLIC_IP"]
description       = "$SG_DESCRIPTION"
EOF

tofu init -input=false

if [ "$ACTION" = "destroy" ]; then
    echo "Revoke security group access for this runner"
    tofu destroy -auto-approve -input=false -var-file="$SG_TFVARS_FILE"
else
    echo "Authorize security group access for this runner"
    tofu apply -auto-approve -input=false -var-file="$SG_TFVARS_FILE"
fi
