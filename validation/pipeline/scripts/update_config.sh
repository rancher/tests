#!/bin/bash

# Set variables
REPO_ROOT=$(pwd)

# If first argument is an env file, load it
if [[ -n "${1:-}" && -f "$1" ]]; then
    echo "Loading overrides from $1"
    set -a 
    source "$1"
    set +a
fi

: "${CONFIG_FILE:=/root/go/src/github.com/rancher/tests/validation/config.yaml}"
: "${EXISTING_RANCHER_HOST}"
: "${RANCHER_API_KEY}"

cd  /root/go/src/github.com/rancher/qa-infra-automation

cat > ansible/rancher/default-ha/generated.tfvars <<EOF
fqdn = "https://$EXISTING_RANCHER_HOST"
api_key = "$RANCHER_API_KEY"
EOF

yq e ".rancher.adminToken = \"$RANCHERAPI_KEY\"" -i $CONFIG_FILE
yq e ".rancher.host = \"$EXISTING_RANCHER_HOST\"" -i $CONFIG_FILE
export hostip=$EXISTING_RANCHER_HOST