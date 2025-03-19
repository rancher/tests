#!/bin/bash
set -e
cd $(dirname $0)/../../../../../rancher/tests

echo "building release upgrade bin"
env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o validation/pipeline/bin/releaseupgrade ./validation/pipeline/releaseupgrade

echo "running release upgrade"
validation/pipeline/bin/releaseupgrade
