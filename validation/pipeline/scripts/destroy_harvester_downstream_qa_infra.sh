#!/bin/bash
set -eux

echo "Destroy Harvester downstream infra"

: "${HARVESTER_TFVARS_FILE:=harvester.tfvars}"
: "${QAINFRA_SCRIPT_PATH:=/root/go/src/github.com/rancher/qa-infra-automation}"
: "${HARVESTER_TOFU_PATH:=tofu/harvester/modules/vm}"
: "${RANCHER_IMPORT_TOFU_PATH:=tofu/rancher/import}"
: "${RANCHER_DEFAULT_PATH:=ansible/rancher/default-ha}"
: "${GENERATED_TFVARS_FILE:=generated.tfvars}"

cd "$QAINFRA_SCRIPT_PATH"

GENERATED_TFVARS_PATH="$QAINFRA_SCRIPT_PATH/$RANCHER_DEFAULT_PATH/$GENERATED_TFVARS_FILE"
DOWNSTREAM_CLUSTER_NAME_FILE="$QAINFRA_SCRIPT_PATH/$RANCHER_IMPORT_TOFU_PATH/downstream_cluster_name.txt"
if [ -d "$QAINFRA_SCRIPT_PATH/$RANCHER_IMPORT_TOFU_PATH" ] && [ -f "$GENERATED_TFVARS_PATH" ] && [ -f "$DOWNSTREAM_CLUSTER_NAME_FILE" ]; then
    DOWNSTREAM_CLUSTER_NAME=$(cat "$DOWNSTREAM_CLUSTER_NAME_FILE")
    if ! tofu -chdir="$RANCHER_IMPORT_TOFU_PATH" destroy -auto-approve -var-file="$GENERATED_TFVARS_PATH" -var "cluster_name=$DOWNSTREAM_CLUSTER_NAME"; then
        echo "Warning: failed to destroy the imported downstream cluster tofu state. It may need to be removed manually from Rancher." >&2
    fi
else
    echo "No downstream cluster import state found to destroy; skipping."
fi

tofu -chdir="$HARVESTER_TOFU_PATH" destroy -auto-approve -var-file="$HARVESTER_TFVARS_FILE"
