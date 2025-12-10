#!/bin/bash
set -e

: "${RANCHER_HOST:?RANCHER_HOST not set}"
: "${RANCHER_ADMIN_PASSWORD:?RANCHER_ADMIN_PASSWORD not set}"

if ! command -v jq &> /dev/null; then
  sudo apt-get update
  sudo apt-get install -y jq
fi

http_code=$(curl -s -k -o response.txt -w "%{http_code}" "https://$RANCHER_HOST/v1-public/login" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json" \
  -d "{\"type\": \"localProvider\", \"username\": \"admin\", \"password\": \"$RANCHER_ADMIN_PASSWORD\", \"responseType\": \"json\"}")

response=$(cat response.txt)
rm -f response.txt

if [[ "$http_code" == "404" ]]; then
    echo "⚠️ v1-public login API not supported, trying v3-public API..."
    response=$(curl -s -k "https://$RANCHER_HOST/v3-public/localProviders/local?action=login" \
      -H "Content-Type: application/json" \
      -H "Accept: application/json" \
      -d "{\"username\":\"admin\", \"password\":\"$RANCHER_ADMIN_PASSWORD\"}")
fi

token=$(echo "$response" | jq -r '.token')
: "${token:?❌ Failed to get Rancher token. Response: $response}"

echo "::add-mask::$token" >&2
echo "$token"
