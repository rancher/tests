#!/bin/bash
set -ex
cd $(dirname $0)/../../../../../

echo "build corral packages"
sh validation/pipeline/scripts/build_corral_packages.sh

echo | corral config

echo "build rancherHA images"
sh validation/pipeline/scripts/build_rancherha_images.sh

corral list

echo "running rancher corral"
validation/registries/bin/rancherha
