#!/bin/bash

# ./setup-provider.sh <provider> <version>

set -e 

# Validate user input
if [ $# -ne 2 ]; then
  echo "Usage: $0 <provider> <giturl>"
  exit 1
fi

PROVIDER=$1
GITURL=$2
VERSION="0.0.0-dev"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

if [ "$OS" = "darwin" ] && [ "$ARCH" = "arm64" ]; then
  PLATFORM="darwin_arm64"
else
  PLATFORM="linux_amd64"
fi

# Download binary
DIR=~/.terraform.d/plugins/terraform.local/${PROVIDER}/${PROVIDER}/${VERSION}/${PLATFORM}
mkdir -p $DIR
(umask u=rwx,g=rwx,o=rwx && mkdir -p $DIR)
git clone $GITURL provider-clone
cd provider-clone
go build -o terraform-provider-${PROVIDER} && \
cp terraform-provider-${PROVIDER} $DIR

# Mod binary
chmod +x ${DIR}/terraform-provider-${PROVIDER}

echo -e "provider ${PROVIDER} ${VERSION} is ready to test!
Please update the required_providers block in your Terraform config file

terraform {
  required_providers {
    rancher2 = {
      source = "terraform.local/${PROVIDER}/${PROVIDER}"
      version = "${VERSION}"
    }
  }
}"
