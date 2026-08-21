#!/bin/bash
set -ex

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
if [ -f "$GENERATED_TFVARS_PATH" ] && [ -f "$DOWNSTREAM_CLUSTER_NAME_FILE" ]; then
    DOWNSTREAM_CLUSTER_NAME=$(cat "$DOWNSTREAM_CLUSTER_NAME_FILE")
    tofu -chdir="$RANCHER_IMPORT_TOFU_PATH" destroy -auto-approve -var-file="$GENERATED_TFVARS_PATH" -var "cluster_name=$DOWNSTREAM_CLUSTER_NAME"
fi

tofu -chdir="$HARVESTER_TOFU_PATH" destroy -auto-approve -var-file=$HARVESTER_TFVARS_FILE