#!/bin/bash
set -ex

echo "Create capi cluster"

: "${QAINFRA_SCRIPT_PATH:=/root/go/src/github.com/rancher/qa-infra-automation}"
: "${CAPI_PLAYBOOK_PATH:=ansible/rancher/downstream/capi}"
: "${CAPI_PLAYBOOK_FILE:=capi-playbook.yml}"
: "${CAPI_VARS_FILE:=vars.yaml}"

cd "$QAINFRA_SCRIPT_PATH/$CAPI_PLAYBOOK_PATH"

ansible-playbook "$CAPI_PLAYBOOK_FILE" -e "@$CAPI_VARS_FILE"
