#!/bin/bash
set -e

if [ -z "$RANCHER_HOST" ] || [ -z "$RANCHER_ADMIN_PASSWORD" ]; then
  echo "❌ RANCHER_HOST or RANCHER_ADMIN_PASSWORD not set"
  exit 1
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

if [ -z "$token" ] || [ "$token" == "null" ]; then
  echo "❌ Failed to get Rancher token. Response: $response"
  exit 1
fi

echo "RANCHER_ADMIN_TOKEN=$token" >> $GITHUB_ENV
echo "::add-mask::$token"
