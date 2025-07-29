#!/bin/bash

set -e  # Enable exit on non-zero exit code

cd  /root/go/src/github.com/rancher/qa-infra-automation

# Set variables (consistent with build_qa_infra.sh)
WORKSPACE_NAME="jenkins_workspace"
TERRAFORM_DIR="tofu/aws/modules/cluster_nodes"
TFVARS_FILE="cluster.tfvars" # Ensure this matches the build script

# --- Terraform Steps ---
echo "--- Terraform Destroy ---"

# Select the Terraform workspace
terraform -chdir="$TERRAFORM_DIR" workspace select "$WORKSPACE_NAME"
if [ $? -ne 0 ]; then
    echo "Error: Terraform workspace select failed."
    exit 1
fi

export TF_WORKSPACE="$WORKSPACE_NAME"

# Destroy the Terraform infrastructure
terraform -chdir="$TERRAFORM_DIR" destroy -auto-approve -var-file="$TFVARS_FILE"
if [ $? -ne 0 ]; then
    echo "Error: Terraform destroy failed."
    exit 1
fi

echo "Terraform infrastructure destroyed successfully!"
