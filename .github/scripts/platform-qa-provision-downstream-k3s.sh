#!/bin/bash
set -e

: "${RANCHER_HOST:?Rancher host not set}"
: "${RANCHER_ADMIN_TOKEN:?Rancher admin token not set}"
: "${K3S_VERSION:?K3s version not set}"
: "${CNI:?CNI not set}"

: "${AWS_REGION:?AWS region not set}"
: "${AWS_AMI:?AWS AMI not set}"
: "${AWS_INSTANCE_TYPE:?AWS instance type not set}"
: "${AWS_ROOT_SIZE:?AWS root size not set}"
: "${AWS_VPC_ID:?AWS VPC ID not set}"
: "${AWS_SUBNET_ID:?AWS Subnet ID not set}"
: "${AWS_SECURITY_GROUPS_JSON:?AWS security groups not set}"
: "${SSH_PRIVATE_KEY_NAME:?AWS SSH key pair name not set}"
: "${AWS_ACCESS_KEY:?AWS access key not set}"
: "${AWS_SECRET_KEY:?AWS secret key not set}"

CLUSTER_NAME="pqanodeprov1"
API="https://$RANCHER_HOST/v3"
MACHINECONFIG_NAME="mc-${CLUSTER_NAME}-pool1"
MACHINECONFIG_FILE="/tmp/${MACHINECONFIG_NAME}.yaml"
CLUSTER_FILE="/tmp/${CLUSTER_NAME}-cluster.yaml"
CLOUD_CREDENTIAL_NAME="aws-creds-$CLUSTER_NAME"
NAMESPACE="fleet-default"

# Check if the cluster already exists
EXISTING_CLUSTER=$(curl -s -k -H "Authorization: Bearer $RANCHER_ADMIN_TOKEN" \
  "$API/clusters?name=$CLUSTER_NAME" | jq -r '.data[0].id // empty')
if [[ -n "$EXISTING_CLUSTER" ]]; then
  echo "⚠️ Cluster '$CLUSTER_NAME' already exists (ID: $EXISTING_CLUSTER). Skipping creation."
  export CLUSTER_NAME="$CLUSTER_NAME"
  exit 0
fi

echo "==========================================="
echo " Provisioning Downstream Cluster"
echo " Rancher: $RANCHER_HOST"
echo " Cluster: $CLUSTER_NAME"
echo "==========================================="
# Generate local cluster kubeconfig
MANAGEMENT_CLUSTER_ID="local"
RANCHER_KUBECONFIG_JSON="/tmp/management_kubeconfig.json"
RANCHER_KUBECONFIG="/tmp/management_kubeconfig.yaml"
curl -s -k -X POST -H "Authorization: Bearer $RANCHER_ADMIN_TOKEN" "$API/clusters/$MANAGEMENT_CLUSTER_ID?action=generateKubeconfig" -o "$RANCHER_KUBECONFIG_JSON"
jq -r '.config' "$RANCHER_KUBECONFIG_JSON" > "$RANCHER_KUBECONFIG"
export RANCHER_KUBECONFIG

# Create Cloud Credential
CLOUD_CREDS_PAYLOAD=$(jq -n \
  --arg name "$CLOUD_CREDENTIAL_NAME" \
  --arg access "$AWS_ACCESS_KEY" \
  --arg secret "$AWS_SECRET_KEY" \
  --arg region "$AWS_REGION" \
  '{
     type: "amazonec2credential",
     name: $name,
     amazonec2credentialConfig: {
       accessKey: $access,
       secretKey: $secret,
       defaultRegion: $region
     }
   }')

CLOUD_CREDENTIAL_ID=$(
  curl -s -k -X POST \
    -H "Authorization: Bearer $RANCHER_ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -d "$CLOUD_CREDS_PAYLOAD" \
    "$API/cloudcredentials" | jq -r '.id'
)

if [[ "$CLOUD_CREDENTIAL_ID" == "null" || -z "$CLOUD_CREDENTIAL_ID" ]]; then
  echo "❌ Failed to create cloud credential"
  exit 1
fi

# Create Machine Config
cat > "$MACHINECONFIG_FILE" <<EOF
apiVersion: rke-machine-config.cattle.io/v1
kind: Amazonec2Config
metadata:
  name: ${MACHINECONFIG_NAME}
  namespace: ${NAMESPACE}
region: ${AWS_REGION}
ami: ${AWS_AMI}
instanceType: ${AWS_INSTANCE_TYPE}
rootSize: "${AWS_ROOT_SIZE}"
vpcId: ${AWS_VPC_ID}
subnetId: ${AWS_SUBNET_ID}
EOF

kubectl --kubeconfig "$RANCHER_KUBECONFIG" apply -f "$MACHINECONFIG_FILE"

# Create Downstream Cluster
cat > "$CLUSTER_FILE" <<EOF
apiVersion: provisioning.cattle.io/v1
kind: Cluster
metadata:
  name: ${CLUSTER_NAME}
  namespace: ${NAMESPACE}
spec:
  cloudCredentialSecretName: "${CLOUD_CREDENTIAL_ID}"
  kubernetesVersion: ${K3S_VERSION}
  rkeConfig:
    machinePools:
      - name: pool1
        quantity: 1
        controlPlaneRole: true
        etcdRole: true
        workerRole: true
        machineConfigRef:
          kind: Amazonec2Config
          name: ${MACHINECONFIG_NAME}
        dynamicSchemaSpec: >-
          {
            "securityGroup": '"$AWS_SECURITY_GROUPS_JSON"',
            "keypairName": "'$SSH_PRIVATE_KEY_NAME'"
          }
    networking:
      stackPreference: dual
    upgradeStrategy:
      controlPlaneConcurrency: "1"
      workerConcurrency: "1"
EOF

kubectl --kubeconfig "$RANCHER_KUBECONFIG" apply -f "$CLUSTER_FILE"

TIMEOUT=900
START=$(date +%s)
until kubectl --kubeconfig "$RANCHER_KUBECONFIG" get cluster "${CLUSTER_NAME}" -n fleet-default -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null | grep -q True; do
    sleep 15
    if (( $(date +%s) - START > TIMEOUT )); then
        echo "❌ Timeout waiting for cluster to be ready"
        exit 1
    fi
done

DOWNSTREAM_CLUSTER_ID=$(kubectl --kubeconfig "$RANCHER_KUBECONFIG" -n fleet-default get cluster "$CLUSTER_NAME" -o jsonpath='{.status.clusterName}')
DOWNSTREAM_KUBECONFIG_JSON="/tmp/downstream_kubeconfig.json"
DOWNSTREAM_KUBECONFIG="/tmp/downstream_kubeconfig.yaml"
DEPLOYMENT="rancher-webhook"
TIMEOUT=900  
WEBHOOK_NAMESPACE="cattle-system"

curl -s -k -X POST -H "Authorization: Bearer $RANCHER_ADMIN_TOKEN" "$API/clusters/$DOWNSTREAM_CLUSTER_ID?action=generateKubeconfig" -o "$DOWNSTREAM_KUBECONFIG_JSON"
jq -r '.config' "$DOWNSTREAM_KUBECONFIG_JSON" > "$DOWNSTREAM_KUBECONFIG"
export DOWNSTREAM_KUBECONFIG

echo "⏳ Waiting for $DEPLOYMENT deployment to be available in $WEBHOOK_NAMESPACE..."
START=$(date +%s)
while true; do
    AVAILABLE=$(kubectl --kubeconfig "$DOWNSTREAM_KUBECONFIG" -n "$WEBHOOK_NAMESPACE" get deployment "$DEPLOYMENT" -o jsonpath='{.status.availableReplicas}' 2>/dev/null || echo "0")

    if [[ "$AVAILABLE" != "0" ]]; then
        break
    fi

    if (( $(date +%s) - START > TIMEOUT )); then
        echo "❌ Timeout waiting for $DEPLOYMENT deployment to be available."
        exit 1
    fi

    sleep 10
done

echo "✅ Downstream cluster created: $CLUSTER_NAME (ID: $DOWNSTREAM_CLUSTER_ID)"
echo "CLUSTER_NAME=$CLUSTER_NAME" >> $GITHUB_ENV
echo "DOWNSTREAM_CLUSTER_ID=$DOWNSTREAM_CLUSTER_ID" >> $GITHUB_ENV
