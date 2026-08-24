#!/bin/bash
set -euo pipefail

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
: "${RANCHER_IMPORT_TOFU_PATH:=tofu/rancher/import}"
: "${GENERATED_TFVARS_FILE:=generated.tfvars}"
: "${DOWNSTREAM_CLUSTER_NAME:=downstream-$(date +%s)}"
: "${ADD_DOWNSTREAM_PLAYBOOK_PATH:=ansible/rke2/airgap/playbooks/deploy/add-downstream-cluster.yml}"
: "${DOWNSTREAM_INVENTORY_FILE:=$QAINFRA_SCRIPT_PATH/downstream_cluster_inventory.ini}"

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
if [ -z "$SSH_KEY_PATH" ]; then
    echo "Error: Failed to retrieve SSH private key path from tofu output."
    exit 1
fi

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

GENERATED_TFVARS_PATH="$QAINFRA_SCRIPT_PATH/$RANCHER_DEFAULT_PATH/$GENERATED_TFVARS_FILE"
if [ ! -f "$GENERATED_TFVARS_PATH" ]; then
    echo "Error: $GENERATED_TFVARS_PATH not found; cannot import downstream cluster."
    exit 1
fi

if [ ! -f "$DOWNSTREAM_INVENTORY_FILE" ]; then
    echo "Error: $DOWNSTREAM_INVENTORY_FILE not found. It must be provided (e.g. written by the Jenkinsfile from a DOWNSTREAM_INVENTORY_CONFIG parameter)."
    exit 1
fi
sed -i "s|\${IP_ADDRESS}|$IP_ADDRESS|g; s|\${SSH_KEY_PATH}|$SSH_KEY_PATH|g" "$DOWNSTREAM_INVENTORY_FILE"
RANCHER_BOOTSTRAP_PASSWORD=$(grep -oP '(?<=bootstrap_password: ").*(?=")' "$QAINFRA_SCRIPT_PATH/$RANCHER_DEFAULT_PATH/$HARVESTER_VARS_FILE")
RANCHER_HOSTNAME=$(grep -oP '(?<=fqdn = ").*(?=")' "$GENERATED_TFVARS_PATH" | sed 's|^https://||')

cd "$QAINFRA_SCRIPT_PATH"
export DOWNSTREAM_CLUSTER_NAME
ansible-playbook "$ADD_DOWNSTREAM_PLAYBOOK_PATH" \
    -i "$DOWNSTREAM_INVENTORY_FILE" \
    -e "target=downstream" \
    -e "rancher_hostname=${RANCHER_HOSTNAME}" \
    -e "rancher_bootstrap_password=${RANCHER_BOOTSTRAP_PASSWORD}" \
    --ssh-common-args="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
if [ $? -ne 0 ] && [[ $CLEANUP == "true" ]]; then
    echo "Error: Downstream cluster import failed."
    tofu -chdir="$QAINFRA_SCRIPT_PATH/$HARVESTER_TOFU_PATH" destroy -auto-approve -var-file="$HARVESTER_TFVARS_FILE"
    if [ $? -ne 0 ]; then
        echo "Error: Tofu destroy failed."
        exit 1
    fi
    echo "Tofu infrastructure destroyed successfully!"
    exit 1
fi

echo "$DOWNSTREAM_CLUSTER_NAME" > "$QAINFRA_SCRIPT_PATH/$RANCHER_IMPORT_TOFU_PATH/downstream_cluster_name.txt"
echo "Downstream cluster '$DOWNSTREAM_CLUSTER_NAME' imported and registered successfully!"
