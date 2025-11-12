#!/bin/bash
set -ex

echo "Create elemental harvester infra"

: "${CLEANUP:=true}"
: "${ELEMENTAL_TFVARS_FILE:=elemental.tfvars}"
: "${ELEMENTAL_TOFU_PATH:=tofu/aws/modules/s3}"
: "${QAINFRA_SCRIPT_PATH:=/root/go/src/github.com/rancher/qa-infra-automation}"
: "${ELEMENTAL_PLAYBOOK_PATH:=ansible/rancher/downstream/elemental/harvester}"
: "${ELEMENTAL_PLAYBOOK_FILE:=elemental-playbook.yml}"
: "${ELEMENTAL_VARS_FILE:=vars.yaml}"

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

cd "$QAINFRA_SCRIPT_PATH/$ELEMENTAL_PLAYBOOK_PATH"

ansible-playbook "$ELEMENTAL_PLAYBOOK_FILE" -e "@$ELEMENTAL_VARS_FILE"
