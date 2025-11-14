#!/bin/bash
set -e

if [ -z "$RANCHER_SHORT_VERSION" ]; then
  echo "❌ RANCHER_SHORT_VERSION not set. Exiting."
  exit 1
fi

RANGE=$(curl -s -H "Authorization: Bearer $RANCHER_ADMIN_TOKEN" \
  "https://$RANCHER_HOST/v1/management.cattle.io.settings/ui-k8s-supported-versions-range" \
  | jq -r .value)

if [ -z "$RANGE" ]; then
  echo "❌ Could not fetch supported Kubernetes version range. Exiting."
  exit 1
fi
MIN_K8S_RAW=$(echo "$RANGE" | awk '{print substr($1,3)}') 
MAX_K8S_RAW=$(echo "$RANGE" | awk '{print substr($2,3)}') 

DATA_URL="https://releases.rancher.com/kontainer-driver-metadata/dev-v$RANCHER_SHORT_VERSION/data.json"
K8S_VERSIONS=$(curl -s "$DATA_URL" \
  | jq -r '.. | .version? // empty' \
  | grep '+k3s' \
  | sed 's/+k3s.*//' \
  | sort -V)
MIN_K8S=$(echo "$K8S_VERSIONS" | grep -E "^${MIN_K8S_RAW%.*}" | head -n1)
MAX_K8S=$(echo "$K8S_VERSIONS" | grep -E "^${MAX_K8S_RAW%.*}" | tail -n1)

K8S_VERSION=$(echo "$K8S_VERSIONS" | sort -V | awk -v min="$MIN_K8S" -v max="$MAX_K8S" '$0 >= min && $0 <= max' | tail -n1)
if [ -z "$K8S_VERSION" ]; then
  echo "❌ No valid K8s version found within supported range. Exiting."
  exit 1
fi

echo "Supported range from Rancher: $RANGE"
echo "Minimum supported K8s version: $MIN_K8S"
echo "Maximum supported K8s version: $MAX_K8S"
echo "K8s version picked for testing: $K8S_VERSION"
echo "K8S_VERSION=${K8S_VERSION// /}" >> $GITHUB_ENV
