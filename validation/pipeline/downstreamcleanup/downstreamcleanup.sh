#!/bin/bash
set -e
cd $(dirname $0)/../../../../../

echo "building release downstream cleanup bin"
env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o validation/pipeline/bin/downstreamcleanup ./validation/pipeline/downstreamcleanup

echo "running downstream cleanup"
validation/pipeline/bin/downstreamcleanup
