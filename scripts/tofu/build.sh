#!/bin/bash

set -x
set -eu

DEBUG="${DEBUG:-false}"
TOFU_VERSION="${TOFU_VERSION:-}"
HARVESTER_PROVIDER_VERSION="${HARVESTER_PROVIDER_VERSION:-}"
RANCHER2_PROVIDER_VERSION="${RANCHER2_PROVIDER_VERSION:-}"

TRIM_JOB_NAME=$(basename "$JOB_NAME")

if [ "false" != "${DEBUG}" ]; then
    echo "Environment:"
    env | sort
fi

count=0
while [[ 3 -gt $count ]]; do
    docker build . -f Dockerfile.infra  --build-arg PEM_FILE=key.pem \
                                        --build-arg TOFU_VERSION="$TOFU_VERSION" \
                                        --build-arg HARVESTER_PROVIDER_VERSION="$HARVESTER_PROVIDER_VERSION" \
                                        --build-arg RANCHER2_PROVIDER_VERSION="$RANCHER2_PROVIDER_VERSION" \
                                        -t infra-validation-"${TRIM_JOB_NAME}""${BUILD_NUMBER}"

    if [[ $? -eq 0 ]]; then break; fi
    count=$(($count + 1))
    echo "Repeating failed Docker build ${count} of 3..."
done
