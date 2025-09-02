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
: "${hostip:=rancher.10.115.5.35.sslip.io}"
: "${API_KEY:=token-t7t2h:vtkt8rr88r724zb2xrtvrls5bqrn59g8tftbknc65hrtdjgw8gk6xn}"

cd  /root/go/src/github.com/rancher/qa-infra-automation

cat > ansible/rancher/default-ha/generated.tfvars <<EOF
fqdn = "https://$hostip"
api_key = "$API_KEY"
EOF

yq e ".rancher.adminToken = \"$API_KEY\"" -i $CONFIG_FILE
yq e ".rancher.host = \"$hostip\"" -i $CONFIG_FILE
cat $CONFIG_FILE
echo $hostip