#!/bin/bash
set -ex

echo "Destroy elemental infra"

: "${ELEMENTAL_TFVARS_FILE:=elemental.tfvars}"
: "${ELEMENTAL_TOFU_PATH:=tofu/gcp/modules/elemental_nodes}"
: "${QAINFRA_SCRIPT_PATH:=/root/go/src/github.com/rancher/qa-infra-automation}"

cd "$QAINFRA_SCRIPT_PATH"

tofu -chdir="$ELEMENTAL_TOFU_PATH" destroy -auto-approve -var-file=$ELEMENTAL_TFVARS_FILE

