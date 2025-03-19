#!/bin/bash
set -e
cd $(dirname $0)/../../../../../rancher/tests

  echo "building qase auto testrun bin"

  env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o validation/testrun ./validation/pipeline/qase/testrun

  if [ $? -ne 0 ]; then
    echo "Failed to build Qase auto testrun binary" >&2
    exit 1
else
    echo "Successfully built Qase auto testrun binary"
fi