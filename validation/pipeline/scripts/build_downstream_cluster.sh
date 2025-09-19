#!/bin/sh

help() {
    cat <<EOF
Usage: $(basename "$0") [options]

This script initializes and applies the Rancher cluster Terraform module
(using tofu), then updates the clusterName in the provided config file.

Environment variables:
  RANCHER_CLUSTER_MODULE_DIR   Path to the Rancher cluster Terraform module
  TERRAFORM_DIR                Path to the main Terraform infrastructure
  TFVARS_FILE                  Path to the main Terraform .tfvars file
  DOWNSTREAM_TFVARS_FILE       Path to the downstream cluster .tfvars file
  GENERATED_TFVARS_FILE        Path to the generated cluster .tfvars file
  CONFIG_FILE                  Path to the YAML config file to update
  CLEANUP                      If set to "true", cleanup is triggered on failure

Options:
  -h, --help                   Show this help message and exit

Examples:
  CLEANUP=true RANCHER_CLUSTER_MODULE_DIR=./modules/rancher/cluster \\
  TERRAFORM_DIR=./infra TFVARS_FILE=vars.tfvars \\
  DOWNSTREAM_TFVARS_FILE=downstream.tfvars GENERATED_TFVARS_FILE=generated.tfvars \\
  CONFIG_FILE=config.yaml ./deploy-rancher.sh

EOF
}

# Check for help flag
if [ "$1" = "-h" ] || [ "$1" = "--help" ]; then
    help
    exit 0
fi

# Init the Rancher downstream cluster module
tofu -chdir="$RANCHER_CLUSTER_MODULE_DIR" init -input=false
if [ $? -ne 0 ] && [[ $CLEANUP == "true" ]]; then
    echo "Error: Terraform init for rancher/cluster module failed. Destroying infrastructure..."
    tofu -chdir="$TERRAFORM_DIR" destroy -auto-approve -var-file="$TFVARS_FILE"
    if [ $? -ne 0 ]; then
        echo "Error: Terraform destroy failed."
        exit 1
    fi
    echo "Terraform infrastructure destroyed successfully!"
    exit 1
fi

# Apply the Rancher downstream cluster module
tofu -chdir="$RANCHER_CLUSTER_MODULE_DIR" apply -auto-approve -var-file="$DOWNSTREAM_TFVARS_FILE" -var-file="$GENERATED_TFVARS_FILE"
if [ $? -ne 0 ] && [[ $CLEANUP == "true" ]]; then
    echo "Error: Terraform apply for rancher/cluster module failed. Destroying infrastructure..."
    tofu -chdir="$RANCHER_CLUSTER_MODULE_DIR" destroy -auto-approve -var-file="$DOWNSTREAM_TFVARS_FILE" -var-file="$GENERATED_TFVARS_FILE"
    if [ $? -ne 0 ]; then
        echo "Warning: Terraform destroy for rancher/cluster module failed. Continuing with main infrastructure cleanup."
    fi
    tofu -chdir="$TERRAFORM_DIR" destroy -auto-approve -var-file="$TFVARS_FILE"
    if [ $? -ne 0 ]; then
        echo "Error: Terraform destroy failed."
        exit 1
    fi
    echo "Terraform infrastructure destroyed successfully!"
    exit 1
fi

# Get the cluster name from tofu output
CLUSTER_NAME=$(tofu -chdir="$RANCHER_CLUSTER_MODULE_DIR" output -raw name)
if [ $? -ne 0 ]; then
    echo "Error: Failed to get cluster name from tofu output."
    exit 1
fi

# Update the clusterName in the main config file
yq e ".rancher.clusterName = \"${CLUSTER_NAME}\"" -i ${CONFIG_FILE}
if [ $? -ne 0 ]; then
    echo "Error: Failed to update clusterName in $CONFIG_FILE"
    exit 1
fi