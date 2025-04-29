#!/bin/bash

cd  /root/go/src/github.com/rancher/qa-infra-automation

# Set variables
REPO_ROOT=$(pwd)
WORKSPACE_NAME="jenkins_workspace"
TERRAFORM_DIR="terraform/aws/cluster_nodes"
RKE2_PLAYBOOK_PATH="ansible/rke2/rke2-playbook.yml"
TERRAFORM_INVENTORY="ansible/rke2/terraform-inventory.yml"
ANSIBLE_CONFIG="ansible/rke2/ansible.cfg"
RANCHER_PLAYBOOK_PATH="ansible/rancher/rancher-playbook.yml"
TFVARS_FILE="cluster.tfvars"
KUBECONFIG_FILE="$REPO_ROOT/ansible/rke2/kubeconfig.yaml"
VARS_FILE="./ansible/vars.yaml"
PRIVATE_KEY_FILE="/root/.ssh/jenkins-elliptic-validation.pem"

# --- Terraform Steps ---
terraform -chdir="$TERRAFORM_DIR" init -input=false

# Create and select the Terraform workspace
terraform -chdir="$TERRAFORM_DIR" workspace new "$WORKSPACE_NAME" || terraform -chdir="$TERRAFORM_DIR" workspace select "$WORKSPACE_NAME"

# Export the TF_WORKSPACE environment variable
export TF_WORKSPACE="$WORKSPACE_NAME"

# Export the ANSIBLE_CONFIG environment variable
export ANSIBLE_CONFIG="$ANSIBLE_CONFIG"

# Export the ANSIBLE_CONFIG environment variable
export ANSIBLE_PRIVATE_KEY_FILE="$PRIVATE_KEY_FILE"

# Apply the Terraform configuration
terraform -chdir="$TERRAFORM_DIR" apply -auto-approve -var-file="$TFVARS_FILE"
if [ $? -ne 0 ]; then
    echo "Error: Terraform apply failed."
    exit 1
fi

# --- RKE2 Playbook ---

# Run the RKE2 playbook with retries
max_attempts=3
attempt=0
while [ $attempt -lt $max_attempts ]; do
    attempt=$((attempt + 1))
    echo "Attempt $attempt: Running RKE2 playbook..."
    ansible-playbook -i "$TERRAFORM_INVENTORY" "$RKE2_PLAYBOOK_PATH" -vvvv -e "@$VARS_FILE"
    rke2_exit_code=$?
    if [ $rke2_exit_code -ne 0 ]; then
        echo "RKE2 playbook failed on attempt $attempt."
        if [ $attempt -lt $max_attempts ]; then
            echo "Waiting 60 seconds before retrying..."
            sleep 60
        fi
    else
        echo "RKE2 playbook succeeded on attempt $attempt."
        break
    fi
done

if [ $attempt -eq $max_attempts ] && [ $rke2_exit_code -ne 0 ]; then
    echo "Error: RKE2 playbook failed after $max_attempts attempts."
    echo "destroy the terraform"
    # Destroy the Terraform infrastructure
    terraform -chdir="$TERRAFORM_DIR" destroy -auto-approve -var-file="$TFVARS_FILE"
    if [ $? -ne 0 ]; then
        echo "Error: Terraform destroy failed."
        exit 1
    fi
    echo "Terraform infrastructure destroyed successfully!"
    exit 1
fi

# Export KUBECONFIG
export KUBECONFIG="$KUBECONFIG_FILE"
echo $KUBECONFIG

# --- Rancher Playbook ---

# Run the Rancher playbook
ansible-playbook "$RANCHER_PLAYBOOK_PATH" -vvvv -e "@$VARS_FILE"
if [ $? -ne 0 ]; then
    echo "Error: Rancher playbook failed."
    terraform -chdir="$TERRAFORM_DIR" destroy -auto-approve -var-file="$TFVARS_FILE"
    if [ $? -ne 0 ]; then
        echo "Error: Terraform destroy failed."
        exit 1
    fi
    echo "Terraform infrastructure destroyed successfully!"
    exit 1
fi

echo "Script completed successfully!"
