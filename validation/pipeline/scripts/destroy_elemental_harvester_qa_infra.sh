#!/bin/bash
set -ex

echo "Destroy elemental harvester infra"

: "${ELEMENTAL_TFVARS_FILE:=elemental.tfvars}"
: "${S3_TOFU_PATH:=tofu/aws/modules/s3}"
: "${QAINFRA_SCRIPT_PATH:=/root/go/src/github.com/rancher/qa-infra-automation}"
: "${HARVESTER_TOFU_PATH:=tofu/harvester/modules/elemental-vm}"

cd "$QAINFRA_SCRIPT_PATH"

tofu -chdir="$S3_TOFU_PATH" destroy -auto-approve -var-file=$ELEMENTAL_TFVARS_FILE

tofu -chdir="$HARVESTER_TOFU_PATH" destroy -auto-approve -var-file=$ELEMENTAL_TFVARS_FILE

