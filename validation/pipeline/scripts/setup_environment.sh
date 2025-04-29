#!/bin/bash
set -ex
cd $(dirname $0)/../../../../../rancher/tests

echo "building rancherversion bin"
env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o tests/v2/validation/rancherversion ./validation/pipeline/rancherversion

# echo "build corral packages"
# sh ./validation/pipeline/scripts/build_corral_packages.sh

# echo | corral config

echo "build rancher infra"
sh ./validation/pipeline/scripts/build_qa_infra.sh

if [ $? -eq 0 ]; then
    echo "build rancherHA images"
    sh ./validation/pipeline/scripts/build_rancherha_images.sh

    if [ $? -eq 0 ]; then
        # corral list

        echo "running rancher"
        validation/registries/bin/rancherha
        if [ $? -ne 0 ]; then
            echo "rancherha failed. Running cleanup script."
            sh ./validation/pipeline/scripts/destroy_qa_infra.sh
            exit 1
        fi
    fi
fi
