#!/bin/bash
set -Eeuo pipefail
# Try to source common helpers if available
for c in "./scripts/lib/common.sh" "../../../../scripts/lib/common.sh" "/root/go/src/github.com/rancher/tests/scripts/lib/common.sh"; do
  if [ -f "$c" ]; then . "$c"; break; fi
done
trap 'echo "setup_environment.sh failed at line $LINENO"' ERR
cd "$(dirname "$0")"/../../../../../rancher/tests

echo "building rancherversion bin"
env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o tests/v2/validation/rancherversion ./validation/pipeline/rancherversion

echo "build rancher infra"
sh ./validation/pipeline/scripts/build_qa_infra.sh
