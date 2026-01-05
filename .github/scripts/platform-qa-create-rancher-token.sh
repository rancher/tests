#!/bin/bash
set -e

: "${RANCHER_HOST:?RANCHER_HOST not set}"
: "${RANCHER_ADMIN_PASSWORD:?RANCHER_ADMIN_PASSWORD not set}"

if ! command -v jq &> /dev/null; then
  sudo apt-get update
  sudo apt-get install -y jq
fi

response=$(curl -sfk "https://$RANCHER_HOST/v3-public/localProviders/local?action=login" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json" \
  -d "{\"username\":\"admin\", \"password\":\"$RANCHER_ADMIN_PASSWORD\"}")

if echo "$response" | jq empty 2>/dev/null; then
    token=$(echo "$response" | jq -r '.token // empty')
else
    token=""
fi

if [ -z "$token" ]; then
    echo "⚠️ v3-public login failed, trying v1-public API..."
    response=$(curl -sfk "https://$RANCHER_HOST/v1-public/login" \
      -H "Content-Type: application/json" \
      -H "Accept: application/json" \
      -d "{\"type\": \"localProvider\", \"username\": \"admin\", \"password\": \"$RANCHER_ADMIN_PASSWORD\", \"responseType\": \"json\"}")
    if echo "$response" | jq empty 2>/dev/null; then
        token=$(echo "$response" | jq -r '.token // empty')
    fi
fi

: "${token:?❌ Failed to get Rancher token. Response: $response}"

echo "::add-mask::$token" >&2
echo "RANCHER_ADMIN_TOKEN=$token" >> "$GITHUB_OUTPUT"
