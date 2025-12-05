#!/bin/bash
set -e

: "${RANCHER_HOST:?RANCHER_HOST not set}"
: "${RANCHER_ADMIN_PASSWORD:?RANCHER_ADMIN_PASSWORD not set}"

if ! command -v jq &> /dev/null; then
  sudo apt-get update
  sudo apt-get install -y jq
fi

response=$(curl -s -k "https://$RANCHER_HOST/v1-public/login" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json" \
  -d "{\"type\": \"localProvider\", \"username\": \"admin\", \"password\": \"$RANCHER_ADMIN_PASSWORD\", \"responseType\": \"json\"}")

token=$(echo "$response" | jq -r '.token')

if [ -z "$token" ] || [ "$token" == "null" ]; then
  echo "⚠️ v1-public login API failed, trying v3-public API to login..."

  response=$(curl -s -k "https://$RANCHER_HOST/v3-public/localProviders/local?action=login" \
    -H "Content-Type: application/json" \
    -H "Accept: application/json" \
    -d "{\"username\":\"admin\", \"password\":\"$RANCHER_ADMIN_PASSWORD\"}")

  token=$(echo "$response" | jq -r '.token')
fi

: "${token:?❌ Failed to get Rancher token. Response: $response}"

echo "::add-mask::$token" >&2
echo "$token"
