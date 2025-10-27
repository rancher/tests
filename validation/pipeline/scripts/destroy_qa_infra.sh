#!/bin/bash

set -e  # Enable exit on non-zero exit code

cd  /root/go/src/github.com/rancher/qa-infra-automation

REPO_ROOT=$(pwd)
# Set variables (consistent with build_qa_infra.sh)
WORKSPACE_NAME="jenkins_workspace"
TERRAFORM_DIR="tofu/aws/modules/cluster_nodes"
RANCHER_CLUSTER_MODULE_DIR="tofu/rancher/cluster"
TFVARS_FILE="cluster.tfvars"
DOWNSTREAM_TFVARS_FILE="downstream-cluster.tfvars"
GENERATED_TFVARS_FILE="$REPO_ROOT/ansible/rancher/default-ha/generated.tfvars"
: "${BUILD_DOWNSTREAM_CLUSTER:=true}"

if [[ "$BUILD_DOWNSTREAM_CLUSTER" == "true" ]]; then
    # --- Rancher Cluster Module Destroy ---
    echo "--- Rancher Cluster Module Destroy ---"

    tofu -chdir="$RANCHER_CLUSTER_MODULE_DIR" init -input=false || echo "Warning: init for rancher/cluster module failed, destroy may fail."

    # Destroy the Rancher cluster module infrastructure
    tofu -chdir="$RANCHER_CLUSTER_MODULE_DIR" destroy -auto-approve -var-file="$DOWNSTREAM_TFVARS_FILE" -var-file="$GENERATED_TFVARS_FILE"
    if [ $? -ne 0 ]; then
        echo "Warning: Terraform destroy for rancher/cluster module failed. Continuing with cleanup."
    fi

fi
# --- Terraform Steps ---
echo "--- Terraform Destroy ---"

# Select the Terraform workspace
tofu -chdir="$TERRAFORM_DIR" workspace select "$WORKSPACE_NAME"
if [ $? -ne 0 ]; then
    echo "Error: Terraform workspace select failed."
    exit 1
fi

export TF_WORKSPACE="$WORKSPACE_NAME"

# Destroy the Terraform infrastructure
tofu -chdir="$TERRAFORM_DIR" destroy -auto-approve -var-file="$TFVARS_FILE"
if [ $? -ne 0 ]; then
    echo "Error: Terraform destroy failed."
    exit 1
fi

echo "Terraform infrastructure destroyed successfully!"
