#!/bin/bash
set -e

if ! command -v jq &> /dev/null; then
  sudo apt-get update
  sudo apt-get install -y jq
fi

VERSION=$(curl -k -s -H "Authorization: Bearer $RANCHER_ADMIN_TOKEN" \
  "https://$RANCHER_HOST/v1/management.cattle.io.settings/server-version" | jq -r '.value // empty')

if [ -z "$VERSION" ]; then
  echo "⚠️ v1 API failed, falling back to v3 API..."
  VERSION=$(curl -k -s -H "Authorization: Bearer $RANCHER_ADMIN_TOKEN" \
    "https://$RANCHER_HOST/v3/settings/server-version" | jq -r '.value // empty')
fi

if [ -z "$VERSION" ]; then
  echo "❌ Unable to fetch Rancher version from v1 or v3 API. Exiting."
  exit 1
fi

echo "Full Rancher version: $VERSION"
echo "RANCHER_FULL_VERSION=$VERSION" >> $GITHUB_ENV

SHORT_VERSION=$(echo "$VERSION" | sed -E 's/^v?([0-9]+\.[0-9]+).*/\1/')
echo "Rancher short version: $SHORT_VERSION"
echo "RANCHER_SHORT_VERSION=$SHORT_VERSION" >> $GITHUB_ENV