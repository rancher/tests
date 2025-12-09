#!/bin/bash
set -euo

echo "Create elemental harvester infra"

: "${CLEANUP:=true}"
: "${ELEMENTAL_TFVARS_FILE:=elemental.tfvars}"
: "${S3_TOFU_PATH:=tofu/aws/modules/s3}"
: "${QAINFRA_SCRIPT_PATH:=/root/go/src/github.com/rancher/qa-infra-automation}"
: "${ELEMENTAL_PLAYBOOK_PATH:=ansible/rancher/downstream/elemental/harvester}"
: "${ELEMENTAL_PLAYBOOK_FILE:=elemental-playbook.yml}"
: "${ELEMENTAL_VARS_FILE:=vars.yaml}"
: "${HARVESTER_TOFU_PATH:=tofu/harvester/modules/elemental-vm}"
: "${ELEMENTAL_TASKS_PATH:=ansible/rancher/downstream/elemental/tasks}"
: "${ELEMENTAL_WAIT_FILE:=wait-elemental-tasks.yml}"

cd "$QAINFRA_SCRIPT_PATH/$S3_TOFU_PATH"

tofu init
tofu apply -auto-approve -var-file=$ELEMENTAL_TFVARS_FILE
if [ $? -ne 0 ] && [[ $CLEANUP == "true" ]]; then
    echo "Error: Playbook failed."
    tofu destroy -auto-approve -var-file="$ELEMENTAL_TFVARS_FILE"
    if [ $? -ne 0 ]; then
        echo "Error: Tofu destroy failed."
        exit 1
    fi
    echo "Tofu infrastructure destroyed successfully!"
    exit 1
fi

cd "$QAINFRA_SCRIPT_PATH/$ELEMENTAL_PLAYBOOK_PATH"

ansible-playbook "$ELEMENTAL_PLAYBOOK_FILE" -e "@$ELEMENTAL_VARS_FILE"

cd "$QAINFRA_SCRIPT_PATH/$HARVESTER_TOFU_PATH"

tofu init
tofu apply -auto-approve -var-file=$ELEMENTAL_TFVARS_FILE
if [ $? -ne 0 ] && [[ $CLEANUP == "true" ]]; then
    echo "Error: Playbook failed."
    tofu destroy -auto-approve -var-file="$ELEMENTAL_TFVARS_FILE"
    if [ $? -ne 0 ]; then
        echo "Error: Tofu destroy failed."
        exit 1
    fi
    echo "Tofu infrastructure destroyed successfully!"
    exit 1
fi

cd "$QAINFRA_SCRIPT_PATH/$ELEMENTAL_TASKS_PATH"

ansible-playbook "$ELEMENTAL_WAIT_FILE" -e "@$ELEMENTAL_VARS_FILE"