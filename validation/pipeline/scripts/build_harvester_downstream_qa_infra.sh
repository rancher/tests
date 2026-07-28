#!/bin/bash
set -uo pipefail

echo "Provisioning Harvester VMs and importing them as a downstream cluster into Rancher"

: "${CLEANUP:=true}"
: "${QAINFRA_SCRIPT_PATH:=/root/go/src/github.com/rancher/qa-infra-automation}"
: "${HARVESTER_TOFU_PATH:=tofu/harvester/modules/vm}"
: "${HARVESTER_TFVARS_FILE:=terraform.tfvars}"
: "${HARVESTER_PLAYBOOK_PATH:=ansible/harvester}"
: "${HARVESTER_PLAYBOOK_FILE:=harvester-downstream-playbook.yml}"
: "${HARVESTER_VARS_FILE:=vars.yaml}"

cd "$QAINFRA_SCRIPT_PATH/$HARVESTER_PLAYBOOK_PATH"

ansible-playbook "$HARVESTER_PLAYBOOK_FILE" -e "@$HARVESTER_VARS_FILE"
status=$?

if [ $status -ne 0 ] && [ "$CLEANUP" == "true" ]; then
    echo "Error: Playbook failed. Destroying Harvester VMs..."
    tofu -chdir="$QAINFRA_SCRIPT_PATH/$HARVESTER_TOFU_PATH" destroy -auto-approve -var-file="$HARVESTER_TFVARS_FILE"
    if [ $? -ne 0 ]; then
        echo "Error: Tofu destroy failed."
        exit 1
    fi
    echo "Tofu infrastructure destroyed successfully!"
fi

exit $status
