#!/bin/bash
set -ex
cd $(dirname $0)/../../../../../rancher/tests

echo "building rancherversion bin"
env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o tests/v2/validation/rancherversion ./validation/pipeline/rancherversion

echo "build corral packages"
sh ./validation/pipeline/scripts/build_corral_packages.sh

echo | corral config

echo "build rancherHA images"
sh ./validation/pipeline/scripts/build_rancherha_images.sh

corral list

echo "running rancher corral"
validation/registries/bin/rancherha
