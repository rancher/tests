#!/bin/bash
set -ex

echo "Upgrade downstream cluster"

: "${QAINFRA_SCRIPT_PATH:=/root/go/src/github.com/rancher/qa-infra-automation}"
: "${UPGRADE_PLAYBOOK_PATH:=ansible/rancher/downstream}"
: "${UPGRADE_PLAYBOOK_FILE:=downstream-upgrade-playbook.yml}"
: "${UPGRADE_VARS_FILE:=vars.yaml}"

cd "$QAINFRA_SCRIPT_PATH/$UPGRADE_PLAYBOOK_PATH"

ansible-playbook "$UPGRADE_PLAYBOOK_FILE" -e "@$UPGRADE_VARS_FILE"
