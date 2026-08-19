#!/bin/bash
set -ex

echo "Destroy Harvester downstream infra"

: "${HARVESTER_TFVARS_FILE:=harvester.tfvars}"
: "${QAINFRA_SCRIPT_PATH:=/root/go/src/github.com/rancher/qa-infra-automation}"
: "${HARVESTER_TOFU_PATH:=tofu/harvester/modules/vm}"

cd "$QAINFRA_SCRIPT_PATH"

tofu -chdir="$HARVESTER_TOFU_PATH" destroy -auto-approve -var-file=$HARVESTER_TFVARS_FILE


