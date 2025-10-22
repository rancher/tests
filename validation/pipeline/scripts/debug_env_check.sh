#!/usr/bin/env bash
set -euo pipefail

REQUIRED_VARS=(AWS_AMI AWS_HOSTNAME_PREFIX AWS_ROUTE53_ZONE AWS_SSH_USER AWS_SECURITY_GROUP AWS_VPC AWS_VOLUME_SIZE AWS_SUBNET INSTANCE_TYPE AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_REGION)

echo "[DEBUG] Checking environment variables presence"
for var in "${REQUIRED_VARS[@]}"; do
  if [ -z "${!var+set}" ]; then
    echo "$var: UNSET"
  else
    echo "$var: SET"
  fi
done

echo "[DEBUG] Checking /tmp/.env presence"
if [ -f /tmp/.env ]; then
  echo "/tmp/.env exists - keys (redacted):"
  awk -F= '{print $1"=REDACTED"}' /tmp/.env | sed -n '1,20p'
else
  echo "/tmp/.env not found"
fi

echo "[DEBUG] Checking QA_INFRA_CLONE_PATH and airgap_lib.sh"
if [ -d /root/qa-infra-automation ]; then
  echo "QA_INFRA_CLONE_PATH: /root/qa-infra-automation exists"
else
  echo "QA_INFRA_CLONE_PATH: /root/qa-infra-automation NOT FOUND"
fi

if [ -f /root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap_lib.sh ]; then
  echo "airgap_lib.sh reachable at /root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap_lib.sh"
else
  echo "airgap_lib.sh NOT FOUND at /root/go/src/github.com/rancher/tests/validation/pipeline/scripts/airgap_lib.sh"
fi

exit 0