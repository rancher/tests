#!/bin/bash
set -ex

echo "Create elemental infra"

: "${CLEANUP:=true}"
: "${ELEMENTAL_TFVARS_FILE:=elemental.tfvars}"
: "${ELEMENTAL_TOFU_PATH:=tofu/gcp/modules/elemental_nodes}"
: "${QAINFRA_SCRIPT_PATH:=/root/go/src/github.com/rancher/qa-infra-automation}"

cd "$QAINFRA_SCRIPT_PATH"

tofu -chdir="$ELEMENTAL_TOFU_PATH" init
tofu -chdir="$ELEMENTAL_TOFU_PATH" apply -auto-approve -var-file=$ELEMENTAL_TFVARS_FILE
if [ $? -ne 0 ] && [[ $CLEANUP == "true" ]]; then
    echo "Error: Playbook failed."
    tofu -chdir="$ELEMENTAL_TOFU_PATH" destroy -auto-approve -var-file="$ELEMENTAL_TFVARS_FILE"
    if [ $? -ne 0 ]; then
        echo "Error: Terraform destroy failed."
        exit 1
    fi
    echo "Terraform infrastructure destroyed successfully!"
    exit 1
fi

