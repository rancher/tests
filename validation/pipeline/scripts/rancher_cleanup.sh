#!/bin/bash
set -ex
cd $(dirname $0)/../../../../../

echo | corral config

corral list

echo "cleanup rancher"
validation/registries/bin/ranchercleanup
