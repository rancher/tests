#!/bin/bash
set -e
cd $(dirname $0)/../../../../../

echo "building rancher HA corral bin"
env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o validation/registries/bin/rancherha ./validation/pipeline/rancherha

echo "building rancher cleanup bin"
env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o validation/registries/bin/ranchercleanup ./validation/pipeline/ranchercleanup
