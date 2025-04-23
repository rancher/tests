#!/bin/bash
set -ex
cd $(dirname $0)/../../../../../rancher/tests

echo | corral config

corral list

ls -al
pwd
echo "cleanup rancher"
validation/registries/bin/ranchercleanup
