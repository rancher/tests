#!/bin/bash

cd  /root/go/src/github.com/rancher/qa-infra-automation

# Set variables
REPO_ROOT=$(pwd)
WORKSPACE_NAME="jenkins_workspace"
TERRAFORM_DIR="tofu/aws/modules/cluster_nodes"
RKE2_PLAYBOOK_PATH="ansible/rke2/default/rke2-playbook.yml"
TERRAFORM_INVENTORY="ansible/rke2/default/terraform-inventory.yml"
TERRAFORM_TEMPLATE="ansible/rke2/default/inventory-template.yml"
ANSIBLE_CONFIG="ansible/rke2/default/ansible.cfg"
RANCHER_PLAYBOOK_PATH="ansible/rancher/default-ha/rancher-playbook.yml"
TFVARS_FILE="cluster.tfvars"
DOWNSTREAM_TFVARS_FILE="downstream-cluster.tfvars"
KUBECONFIG_FILE="$REPO_ROOT/ansible/rke2/default/kubeconfig.yaml"
GENERATED_TFVARS_FILE="$REPO_ROOT/ansible/rancher/default-ha/generated.tfvars"
RANCHER_CLUSTER_MODULE_DIR="tofu/rancher/cluster"
VARS_FILE="./ansible/vars.yaml"
PRIVATE_KEY_FILE="/root/.ssh/jenkins-elliptic-validation.pem"
TERRAFORM_NODE_SOURCE="tofu/aws/modules/cluster_nodes"

# --- Terraform Steps ---
terraform -chdir="$TERRAFORM_DIR" init -input=false

# Create and select the Terraform workspace
terraform -chdir="$TERRAFORM_DIR" workspace new "$WORKSPACE_NAME" || terraform -chdir="$TERRAFORM_DIR" workspace select "$WORKSPACE_NAME"

# Export the TF_WORKSPACE environment variable
export TF_WORKSPACE="$WORKSPACE_NAME"

# Export the TERRAFORM_NODE_SOURCE environment variable
export TERRAFORM_NODE_SOURCE="$TERRAFORM_NODE_SOURCE"

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

envsubst < "$TERRAFORM_TEMPLATE" > "$TERRAFORM_INVENTORY"

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


# --- Rancher Cluster Module ---

# Init the Rancher cluster module
terraform -chdir="$RANCHER_CLUSTER_MODULE_DIR" init -input=false
if [ $? -ne 0 ]; then
    echo "Error: Terraform init for rancher/cluster module failed. Destroying infrastructure..."
    terraform -chdir="$TERRAFORM_DIR" destroy -auto-approve -var-file="$TFVARS_FILE"
    if [ $? -ne 0 ]; then
        echo "Error: Terraform destroy failed."
        exit 1
    fi
    echo "Terraform infrastructure destroyed successfully!"
    exit 1
fi

# Apply the Rancher cluster module
terraform -chdir="$RANCHER_CLUSTER_MODULE_DIR" apply -auto-approve -var-file="$DOWNSTREAM_TFVARS_FILE" -var-file="$GENERATED_TFVARS_FILE"
if [ $? -ne 0 ]; then
    echo "Error: Terraform apply for rancher/cluster module failed. Destroying infrastructure..."
    terraform -chdir="$RANCHER_CLUSTER_MODULE_DIR" destroy -auto-approve -var-file="$DOWNSTREAM_TFVARS_FILE" -var-file="$GENERATED_TFVARS_FILE"
    if [ $? -ne 0 ]; then
        echo "Warning: Terraform destroy for rancher/cluster module failed. Continuing with main infrastructure cleanup."
    fi
    terraform -chdir="$TERRAFORM_DIR" destroy -auto-approve -var-file="$TFVARS_FILE"
    if [ $? -ne 0 ]; then
        echo "Error: Terraform destroy failed."
        exit 1
    fi
    echo "Terraform infrastructure destroyed successfully!"
    exit 1
fi

# Get the cluster name from terraform output
CLUSTER_NAME=$(terraform -chdir="$RANCHER_CLUSTER_MODULE_DIR" output -raw name)
if [ $? -ne 0 ]; then
    echo "Error: Failed to get cluster name from terraform output."
    exit 1
fi

# Update the clusterName in the main config file
CONFIG_FILE="/root/go/src/github.com/rancher/tests/validation/config.yaml"
yq e ".rancher.clusterName = \"${CLUSTER_NAME}\"" -i ${CONFIG_FILE}
if [ $? -ne 0 ]; then
    echo "Error: Failed to update clusterName in $CONFIG_FILE"
    exit 1
fi


# Get the admin token from the generated tfvars file
ADMIN_TOKEN=$(grep 'api_key' "$GENERATED_TFVARS_FILE" | awk -F'"' '{print $2}')
if [ -z "$ADMIN_TOKEN" ]; then
    echo "Error: Failed to get api_key from $GENERATED_TFVARS_FILE."
    exit 1
fi

# Update the adminToken in the main config file
yq e ".rancher.adminToken = \"${ADMIN_TOKEN}\"" -i ${CONFIG_FILE}
if [ $? -ne 0 ]; then
    echo "Error: Failed to update adminToken in $CONFIG_FILE"
    exit 1
fi

echo "Script completed successfully!"
