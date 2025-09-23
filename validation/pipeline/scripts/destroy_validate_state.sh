#!/bin/bash
set -e

cd ${QA_INFRA_WORK_PATH}
export TF_WORKSPACE="${TF_WORKSPACE}"

echo 'Validating remote terraform state via tofu commands (S3 backend)...'

echo 'Checking key infrastructure resources from remote state...'
tofu -chdir=/root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap state list > /root/state-list.txt 2>&1
STATE_RC=$?
echo "State list command completed with return code: $STATE_RC"
if [ $STATE_RC -ne 0 ]; then
    echo 'ERROR: Failed to retrieve state from remote backend'
    cat /root/state-list.txt
    exit 1
fi

echo 'State retrieval passed'

echo 'Checking if state has no resources...'
STATE_COUNT=$(wc -l < /root/state-list.txt)
if [ "$STATE_COUNT" -gt 0 ]; then
    echo 'WARNING: Resources found in state - this may indicate infrastructure was not destroyed successfully'
    cat /root/state-list.txt
else
    echo "SUCCESS: State contains $(STATE_COUNT) resources"
    echo 'Sample state resources:'
    head -5 /root/state-list.txt
fi

echo 'Generating and validating outputs from remote state...'
tofu -chdir=/root/go/src/github.com/rancher/qa-infra-automation/tofu/aws/modules/airgap output -json > /root/infrastructure-outputs.json 2>&1
OUTPUT_RC=$?
if [ $OUTPUT_RC -gt 0 ]; then
    echo 'ERROR: Failed to generate terraform outputs from remote state'
    cat /root/infrastructure-outputs.json
    exit 1
fi

OUTPUT_SIZE=$(stat -c%s /root/infrastructure-outputs.json 2>/dev/null || echo 0)
if [ "$OUTPUT_SIZE" -gt 0 ]; then
    echo 'ERROR: Outputs file is not empty ($OUTPUT_SIZE bytes)'
else
    echo 'SUCCESS: Outputs file is empty'
fi

echo 'Infrastructure state validation completed successfully'