#!/bin/bash
set -e

OS=$(uname | tr '[:upper:]' '[:lower:]')
cd $(dirname $0)/../../../

if [[ -z "${QASE_TEST_RUN_ID}" ]]; then
  echo "No QASE test run ID provided"
elif [[ -z "${QASE_PROJECT_ID}" ]]; then
  echo "No QASE project ID provided"
else
  echo "Building QASE reporter binary"
  echo $(env GOOS=${OS} GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false -o validation/reporter ./validation/pipeline/qase/reporter-v2)
fi