#!/bin/bash

cd /root/go/src/github.com/rancher/qa-infra-automation/
REPO_ROOT=$(pwd)

: "${CONFIG_FILE:=/root/go/src/github.com/rancher/tests/validation/config.yaml}"
: "${TFVARS_FILE:=cluster.tfvars}"
: "${GENERATED_TFVARS_FILE:=$REPO_ROOT/ansible/rancher/default-ha/generated.tfvars}"



tofu -chdir="tofu/rancher/cluster" init
tofu -chdir="tofu/rancher/cluster" apply -auto-approve -var-file=$TFVARS_FILE -var-file=$GENERATED_TFVARS_FILE
DOWNSTREAM_CLUSTER_NAME=$(tofu -chdir="tofu/rancher/cluster" output -raw name)

yq e ".rancher.clusterName = \"$DOWNSTREAM_CLUSTER_NAME\"" -i "$CONFIG_FILE"