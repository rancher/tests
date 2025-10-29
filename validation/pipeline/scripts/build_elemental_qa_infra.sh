#!/bin/bash
set -ex

echo "Create elemental infra"

: "${CLEANUP:=true}"
: "${ELEMENTAL_TFVARS_FILE:=elemental.tfvars}"
: "${ELEMENTAL_TOFU_PATH:=tofu/gcp/modules/elemental_nodes}"
: "${QAINFRA_SCRIPT_PATH:=/root/go/src/github.com/rancher/qa-infra-automation}"
: "${ELEMENTAL_PLAYBOOK_PATH:=ansible/rancher/downstream/elemental}"
: "${ELEMENTAL_PLAYBOOK_FILE:=elemental-playbook.yml}"
: "${ELEMENTAL_VARS_FILE:=vars.yaml}"
: "${ELEMENTAL_KEY_FILE:=private_key.pem}"

cd "$QAINFRA_SCRIPT_PATH/$ELEMENTAL_TOFU_PATH"

tofu init
tofu apply -auto-approve -var-file=$ELEMENTAL_TFVARS_FILE
if [ $? -ne 0 ] && [[ $CLEANUP == "true" ]]; then
    echo "Error: Playbook failed."
    tofu destroy -auto-approve -var-file="$ELEMENTAL_TFVARS_FILE"
    if [ $? -ne 0 ]; then
        echo "Error: Terraform destroy failed."
        exit 1
    fi
    echo "Terraform infrastructure destroyed successfully!"
    exit 1
fi

chmod 600 $ELEMENTAL_KEY_FILE

export ELEMENTAL_NODE_IP=$(tofu output -raw public_dns)

cd "$QAINFRA_SCRIPT_PATH/$ELEMENTAL_PLAYBOOK_PATH"

ansible-playbook "$ELEMENTAL_PLAYBOOK_FILE" -vvvv -e "@$ELEMENTAL_VARS_FILE" --extra-vars "elemental_node_public_ip=$ELEMENTAL_NODE_IP" -i inventory.yml
