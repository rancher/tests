#!/bin/bash
set -e

: "${RANCHER_HOST:?RANCHER_HOST not set}"
: "${RANCHER_ADMIN_TOKEN:?RANCHER_ADMIN_TOKEN not set}"
: "${RANCHER_SHORT_VERSION:?RANCHER_SHORT_VERSION not set}"

# Fetch supported K8s range from Rancher
RANGE=$(curl -sfk -H "Authorization: Bearer $RANCHER_ADMIN_TOKEN" -H "Accept: application/json"  \
  "https://$RANCHER_HOST/v1/management.cattle.io.settings/ui-k8s-supported-versions-range" \
  | jq -r .value)

: "${RANGE:?Could not fetch supported Kubernetes version range}"

MIN_K8S_RAW=$(echo "$RANGE" | awk '{print substr($1,3)}') 
MAX_K8S_RAW=$(echo "$RANGE" | awk '{print substr($2,3)}') 

DATA_URL="https://releases.rancher.com/kontainer-driver-metadata/dev-v$RANCHER_SHORT_VERSION/data.json"
ALL_VERSIONS=$(curl -sf "$DATA_URL" | jq -r '.. | .version? // empty')

# Get latest RKE2 and K3S versions
RKE2_VERSION=$(echo "$ALL_VERSIONS" | egrep 'rke2' | sort -V | tail -n1)
K3S_VERSION=$(echo "$ALL_VERSIONS" | egrep 'k3s'  | sort -V | tail -n1)

K8S_VERSIONS=$(echo "$ALL_VERSIONS" | egrep 'k3s' | sed 's/+k3s.*//' | sort -V)
MIN_K8S=$(echo "$K8S_VERSIONS" | grep -E "^${MIN_K8S_RAW%.*}" | head -n1)
MAX_K8S=$(echo "$K8S_VERSIONS" | grep -E "^${MAX_K8S_RAW%.*}" | tail -n1)
if [ -z "$MAX_K8S" ]; then
  MAX_K8S=$(echo "$K8S_VERSIONS" | tail -n1)
fi

K8S_VERSION=$(echo "$K8S_VERSIONS" | awk -v min="$MIN_K8S" -v max="$MAX_K8S" '$0 >= min && $0 <= max' | tail -n1)
if [ -z "$K8S_VERSION" ]; then
  echo "❌ No valid K8s version found within supported range. Exiting."
  exit 1
fi

echo "Supported range from Rancher: $RANGE"
echo "K8s version picked for testing: $K8S_VERSION"
echo "RKE2 version: $RKE2_VERSION"
echo "K3S version: $K3S_VERSION"

echo "RKE2_VERSION=${RKE2_VERSION}" >> $GITHUB_ENV
echo "K3S_VERSION=${K3S_VERSION}" >> $GITHUB_ENV
echo "KUBERNETES_VERSION=${K3S_VERSION}" >> $GITHUB_ENV
