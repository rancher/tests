#!/bin/bash
set -e

: "${RANCHER_HOST:?Rancher host not set}"
: "${RANCHER_ADMIN_TOKEN:?Rancher admin token not set}"
: "${K3S_VERSION:?K3s version not set}"

: "${AWS_REGION:?AWS region not set}"
: "${AWS_AMI:?AWS AMI not set}"
: "${AWS_INSTANCE_TYPE:?AWS instance type not set}"
: "${AWS_ROOT_SIZE:?AWS root size not set}"
: "${AWS_VPC_ID:?AWS VPC ID not set}"
: "${AWS_SUBNET_ID:?AWS Subnet ID not set}"
: "${AWS_SECURITY_GROUP_NAMES:?AWS security groups not set}"
: "${AWS_ACCESS_KEY:?AWS access key not set}"
: "${AWS_SECRET_KEY:?AWS secret key not set}"
: "${CLUSTER_NAME:?Cluster name not set}"
: "${NETWORK_STACK_PREFERENCE:?Network stack preference not set}"
: "${QA_PRIVATE_REGISTRY_NAME:?QA private registry name not set}"
: "${DOCKERHUB_USERNAME:?DockerHub username not set}"
: "${DOCKERHUB_PASSWORD:?DockerHub password not set}"

API="https://$RANCHER_HOST/v3"
MACHINECONFIG_NAME="mc-${CLUSTER_NAME}-pool1"
MACHINECONFIG_FILE="/tmp/${MACHINECONFIG_NAME}.yaml"
CLUSTER_FILE="/tmp/${CLUSTER_NAME}-cluster.yaml"
CLOUD_CREDENTIAL_NAME="aws-creds-$CLUSTER_NAME"
NAMESPACE="fleet-default"

# Check if the cluster already exists
EXISTING_CLUSTER=$(curl -sfk -H "Authorization: Bearer $RANCHER_ADMIN_TOKEN" -H "Accept: application/json" "$API/clusters?name=$CLUSTER_NAME" | jq -r '.data[0].id // empty')
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
curl -sfk -X POST -H "Authorization: Bearer $RANCHER_ADMIN_TOKEN" -H "Accept: application/json" "$API/clusters/$MANAGEMENT_CLUSTER_ID?action=generateKubeconfig" -o "$RANCHER_KUBECONFIG_JSON"
jq -r '.config' "$RANCHER_KUBECONFIG_JSON" > "$RANCHER_KUBECONFIG"

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
  curl -sfk -X POST \
    -H "Authorization: Bearer $RANCHER_ADMIN_TOKEN" \
    -H "Content-Type: application/json" \
    -d "$CLOUD_CREDS_PAYLOAD" \
    "$API/cloudcredentials" | jq -r '.id'
)

if [[ "$CLOUD_CREDENTIAL_ID" == "null" || -z "$CLOUD_CREDENTIAL_ID" ]]; then
  echo "❌ Failed to create cloud credential"
  exit 1
fi
echo "✅ Cloud credential created with ID: $CLOUD_CREDENTIAL_ID"

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
securityGroup: [${AWS_SECURITY_GROUP_NAMES}]
EOF

kubectl --kubeconfig "$RANCHER_KUBECONFIG" apply -f "$MACHINECONFIG_FILE"
sleep 5

REGISTRY_SECRET_NAME="docker-auth-${CLUSTER_NAME}"
cat <<EOF | kubectl --kubeconfig "$RANCHER_KUBECONFIG" apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: ${REGISTRY_SECRET_NAME}
  namespace: ${NAMESPACE}
type: kubernetes.io/basic-auth
stringData:
  username: "${DOCKERHUB_USERNAME}"
  password: "${DOCKERHUB_PASSWORD}"
EOF

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
    registries:
      mirrors:
        "docker.io":
          endpoint: ["https://${QA_PRIVATE_REGISTRY_NAME}"]
      configs:
        "${QA_PRIVATE_REGISTRY_NAME}":
          authConfigSecretName: "${REGISTRY_SECRET_NAME}"
    machinePools:
      - name: pool1
        quantity: 1
        controlPlaneRole: true
        etcdRole: true
        workerRole: true
        machineConfigRef:
          kind: Amazonec2Config
          name: ${MACHINECONFIG_NAME}
    networking:
      stackPreference: "${NETWORK_STACK_PREFERENCE}"
    upgradeStrategy:
      controlPlaneConcurrency: "1"
      workerConcurrency: "1"
EOF

kubectl --kubeconfig "$RANCHER_KUBECONFIG" apply -f "$CLUSTER_FILE"
echo "⏳ Waiting for cluster '$CLUSTER_NAME' to be ready..."
TIMEOUT_CLUSTER=1200
START_CLUSTER=$(date +%s)
while true; do
    STATUS=$(kubectl --kubeconfig "$RANCHER_KUBECONFIG" get cluster "$CLUSTER_NAME" -n $NAMESPACE -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "Unknown")
    echo "   Cluster status: ${STATUS:-<unknown>}"
    if [[ "$STATUS" == "True" ]]; then
        echo "✅ Cluster '$CLUSTER_NAME' is Ready!"
        break
    fi
    sleep 15
    if (( $(date +%s) - START_CLUSTER > TIMEOUT_CLUSTER )); then
        echo "❌ Timeout waiting for cluster '$CLUSTER_NAME' to be Ready"
        exit 1
    fi
done

DOWNSTREAM_CLUSTER_ID=$(curl -sfk -H "Authorization: Bearer $RANCHER_ADMIN_TOKEN" -H "Accept: application/json" "$API/clusters?name=$CLUSTER_NAME" | jq -r '.data[0].id // empty')
if [[ -z "$DOWNSTREAM_CLUSTER_ID" ]]; then
    echo "❌ Failed to get downstream cluster ID from Rancher API"
    exit 1
fi
DOWNSTREAM_KUBECONFIG_JSON="/tmp/downstream_kubeconfig.json"
DOWNSTREAM_KUBECONFIG="/tmp/downstream_kubeconfig.yaml" 

curl -sfk -X POST -H "Authorization: Bearer $RANCHER_ADMIN_TOKEN" -H "Accept: application/json" "$API/clusters/$DOWNSTREAM_CLUSTER_ID?action=generateKubeconfig" -o "$DOWNSTREAM_KUBECONFIG_JSON"
jq -r '.config' "$DOWNSTREAM_KUBECONFIG_JSON" > "$DOWNSTREAM_KUBECONFIG"

NAMESPACE="cattle-system"
TIMEOUT_DEPLOY=900
START_DEPLOY=$(date +%s)
DEPLOYMENT="rancher-webhook"
echo "⏳ Waiting for $DEPLOYMENT deployment to be available in $NAMESPACE..."
while true; do
    AVAILABLE=$(kubectl --kubeconfig "$DOWNSTREAM_KUBECONFIG" -n "$NAMESPACE" get deployment "$DEPLOYMENT" -o jsonpath='{.status.availableReplicas}' 2>/dev/null || echo "0")
    if (( AVAILABLE >= 1 )); then break; fi
    sleep 10
    if (( $(date +%s) - START_DEPLOY > TIMEOUT_DEPLOY )); then
        echo "❌ Timeout waiting for $DEPLOYMENT deployment to be available."
        exit 1
    fi
done
echo "✅ $DEPLOYMENT deployment is available."

TIMEOUT_DEPLOY=300
START_DEPLOY=$(date +%s)
DEPLOYMENT="system-upgrade-controller"
echo "⏳ Waiting for $DEPLOYMENT deployment to be available in $NAMESPACE..."
while true; do
    AVAILABLE=$(kubectl --kubeconfig "$DOWNSTREAM_KUBECONFIG" -n "$NAMESPACE" get deployment "$DEPLOYMENT" -o jsonpath='{.status.availableReplicas}' 2>/dev/null || echo "0")
    if (( AVAILABLE >= 1 )); then break; fi
    sleep 10
    if (( $(date +%s) - START_DEPLOY > TIMEOUT_DEPLOY )); then
        echo "❌ Timeout waiting for $DEPLOYMENT deployment to be available."
        exit 1
    fi
done
echo "✅ $DEPLOYMENT deployment is available."

echo "✅ Downstream cluster created: $CLUSTER_NAME (ID: $DOWNSTREAM_CLUSTER_ID)"
echo "CLUSTER_NAME=$CLUSTER_NAME" >> $GITHUB_ENV
echo "DOWNSTREAM_CLUSTER_ID=$DOWNSTREAM_CLUSTER_ID" >> $GITHUB_ENV

cleanup() {
    rm -f "$MACHINECONFIG_FILE" "$CLUSTER_FILE" "$RANCHER_KUBECONFIG_JSON" "$DOWNSTREAM_KUBECONFIG_JSON"
}
trap cleanup EXIT
