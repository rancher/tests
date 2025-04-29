#!/bin/bash
set -ex
cd $(dirname $0)/../../../../../rancher/tests

# echo | corral config

# corral list

ls -al
pwd
echo "cleanup rancher"
sh ./validation/pipeline/scripts/destroy_qa_infra.sh
# validation/registries/bin/ranchercleanup

