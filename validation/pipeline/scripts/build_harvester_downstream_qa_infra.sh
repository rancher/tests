#!/bin/bash
set -uo pipefail

echo "Provisioning Harvester VMs and importing them as a downstream cluster into Rancher"

: "${CLEANUP:=true}"
: "${HARVESTER_TFVARS_FILE:=harvester.tfvars}"
: "${QAINFRA_SCRIPT_PATH:=/root/go/src/github.com/rancher/qa-infra-automation}"
: "${HARVESTER_TOFU_PATH:=tofu/harvester/modules/vm}"
: "${RANCHER_PLAYBOOK_PATH:=ansible/harvester/harvester-rancher-playbook.yml}"
: "${HARVESTER_VARS_FILE:=vars.yaml}"
: "${RANCHER_DEFAULT_PATH:=ansible/rancher/default-ha}"
: "${RKE2_DEFAULT_PATH:=ansible/rke2/default}"
: "${RKE2_INVENTORY_FILE:=inventory/inventory.yml}"

cd "$QAINFRA_SCRIPT_PATH/$HARVESTER_TOFU_PATH"

tofu init
tofu apply -auto-approve -var-file=$HARVESTER_TFVARS_FILE
if [ $? -ne 0 ] && [[ $CLEANUP == "true" ]]; then
    echo "Error: Tofu failed."
    tofu destroy -auto-approve -var-file="$HARVESTER_TFVARS_FILE"
    if [ $? -ne 0 ]; then
        echo "Error: Tofu destroy failed."
        exit 1
    fi
    echo "Tofu infrastructure destroyed successfully!"
    exit 1
fi

IP_ADDRESS=$(tofu output -raw kube_api_host)
if [ -z "$IP_ADDRESS" ]; then
    echo "Error: Failed to retrieve VM IP address from tofu output."
    exit 1
fi
echo "Provisioned VM IP address: $IP_ADDRESS"

SSH_KEY_PATH=$(tofu output -raw ssh_private_key_path)

cd "$QAINFRA_SCRIPT_PATH/$RKE2_DEFAULT_PATH"
sed -i "s|\${IP_ADDRESS}|$IP_ADDRESS|g; s|\${SSH_KEY_PATH}|$SSH_KEY_PATH|g" "$HARVESTER_VARS_FILE" "$RKE2_INVENTORY_FILE"

cd "$QAINFRA_SCRIPT_PATH/$RANCHER_DEFAULT_PATH"
sed -i "s|\${IP_ADDRESS}|$IP_ADDRESS|g" "$HARVESTER_VARS_FILE"

cd "$QAINFRA_SCRIPT_PATH"
ansible-playbook "$RANCHER_PLAYBOOK_PATH" -i "$RKE2_DEFAULT_PATH/$RKE2_INVENTORY_FILE"
if [ $? -ne 0 ] && [[ $CLEANUP == "true" ]]; then
    echo "Error: Ansible playbook failed."
    tofu -chdir="$QAINFRA_SCRIPT_PATH/$HARVESTER_TOFU_PATH" destroy -auto-approve -var-file="$HARVESTER_TFVARS_FILE"
    if [ $? -ne 0 ]; then
        echo "Error: Tofu destroy failed."
        exit 1
    fi
    echo "Tofu infrastructure destroyed successfully!"
    exit 1
fi
