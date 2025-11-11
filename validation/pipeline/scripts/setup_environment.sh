#!/bin/bash
set -ex
cd $(dirname $0)/../../../../../rancher/tests

echo "building rancherversion bin"
env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o tests/v2/validation/rancherversion ./validation/pipeline/rancherversion

echo "build rancher infra"
sh ./validation/pipeline/scripts/build_qa_infra.sh
