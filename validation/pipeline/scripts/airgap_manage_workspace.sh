#!/bin/bash
set -e

cd ${QA_INFRA_WORK_PATH}
export TF_WORKSPACE="${TF_WORKSPACE}"

echo 'Managing workspace state...'

echo 'Current workspaces:'
tofu -chdir=tofu/aws/modules/airgap workspace list

echo 'Creating or selecting workspace: ${TF_WORKSPACE}'
if ! tofu -chdir=tofu/aws/modules/airgap workspace select "${TF_WORKSPACE}" 2>/dev/null; then
    echo 'Workspace does not exist, creating new workspace...'
    tofu -chdir=tofu/aws/modules/airgap workspace new "${TF_WORKSPACE}"

    if ! tofu -chdir=tofu/aws/modules/airgap workspace select "${TF_WORKSPACE}"; then
        echo 'ERROR: Failed to create and select workspace'
        exit 1
    fi
fi

# Verify workspace selection
CURRENT_WORKSPACE=$(tofu -chdir=tofu/aws/modules/airgap workspace show)
echo "Current workspace: $CURRENT_WORKSPACE"

# Strip whitespace and handle empty responses
CURRENT_WORKSPACE=$(echo "$CURRENT_WORKSPACE" | xargs)

if [ "$CURRENT_WORKSPACE" = "" ]; then
    echo 'ERROR: Workspace show command returned empty response'
    tofu -chdir=tofu/aws/modules/airgap workspace list
    exit 1
fi

if [ "$CURRENT_WORKSPACE" != "${TF_WORKSPACE}" ]; then
    echo "ERROR: Expected workspace ${TF_WORKSPACE}, but got '$CURRENT_WORKSPACE'"
    echo 'Available workspaces:'
    tofu -chdir=tofu/aws/modules/airgap workspace list
    exit 1
fi

export TF_WORKSPACE="${TF_WORKSPACE}"
echo "Workspace management completed: $TF_WORKSPACE"

tofu -chdir=tofu/aws/modules/airgap init -input=false -upgrade