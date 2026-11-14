#!/bin/bash

set -x
set -eu

DEBUG="${DEBUG:-false}"

env | egrep '^(ARM_|CATTLE_|ADMIN|USER|DO|RANCHER_|AWS_|DEBUG|LOGLEVEL|DEFAULT_|OS_|DOCKER_|CLOUD_|KUBE|BUILD_NUMBER|JOB_NAME|AZURE|TEST_|QASE_|SLACK_|APPCO_|BUILD_URL).*\=.+' | sort > .env

if [ -n "${JOB_NAME:-}" ]; then
    CI_JOB=$(printf '%s' "${JOB_NAME}" | tr -c 'A-Za-z0-9 _.:/=+@-' '-')
    printf 'TF_VAR_aws_default_tags={"ci_job":"%s","ci_build":"%s"}\n' "${CI_JOB}" "${BUILD_NUMBER:-}" >> .env
fi

if [ "false" != "${DEBUG}" ]; then
    cat .env
fi