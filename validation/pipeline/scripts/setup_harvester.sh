#!/bin/bash
set -euo pipefail

EXISTING_HARVESTER_IP="${EXISTING_HARVESTER_IP:-}"
SSH_KEY_FILE=".ssh/$AWS_SSH_KEY_NAME"
SSH_OPTS="-n -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15"
harvester_node_ip="$EXISTING_HARVESTER_IP"
harvester_vip=""

wait_for() {
    local desc="$1" timeout="$2" interval="$3"
    local deadline=$(( SECONDS + timeout ))
    shift 3
    until "$@"; do
        if (( SECONDS >= deadline )); then
            echo "FATAL: timed out after ${timeout}s waiting for: ${desc}" >&2
            return 1
        fi
        sleep "$interval"
    done
    echo "ready: ${desc}"
}

get_pxe_address() {
    harvester_node_ip=$(kubectl get -n tink-system "inventories/$HARVESTER_INVENTORY_NODE" -o jsonpath='{.status.pxeBootConfig.address}')
    [[ -n "$harvester_node_ip" ]]
}

fetch_harvester_kubeconfig() {
    timeout 60 ssh $SSH_OPTS -i "$SSH_KEY_FILE" "rancher@$harvester_node_ip" \
        'sudo cat /etc/rancher/rke2/rke2.yaml' > harvester.yaml 2>/dev/null
    [[ -s harvester.yaml ]]
}

get_vip() {
    harvester_vip=$(timeout 60 ssh $SSH_OPTS -i "$SSH_KEY_FILE" "rancher@$harvester_node_ip" \
        'ip a | grep "/32" | grep -v flannel | head -1' | awk '{print $2}' | cut -d / -f 1)
    [[ -n "$harvester_vip" ]]
}

seeder_cluster_running() {
    local cluster_status
    cluster_status=$(kubectl get "clusters.metal/$HARVESTER_CLUSTER_NAME" -n tink-system -o jsonpath='{.status.status}')
    harvester_vip=$(kubectl get "clusters.metal/$HARVESTER_CLUSTER_NAME" -n tink-system -o jsonpath='{.status.clusterAddress}')
    [[ "$cluster_status" == "clusterRunning" && -n "$harvester_vip" ]]
}

deployment_exists() {
    kubectl get deployment -n "$1" "$2" >/dev/null 2>&1
}

harvester_is_healthy() {
    [[ "$(curl -s -L --insecure --max-time 30 -o /dev/null -w "%{http_code}" "https://$harvester_vip/v3-public/localproviders/local")" == "200" ]]
}

harvester_login() {
    local pw loginBody
    for pw in "password1234" "admin"; do
        loginBody=$(jq -n --arg p "$pw" '{username:"admin", password:$p, responseType:"json"}')
        jsonOutput=$(curl -s --insecure --max-time 60 -d "$loginBody" "https://$harvester_vip/v3-public/localproviders/local?action=login" || true)
        token=$(echo "$jsonOutput" | jq -cr .token 2>/dev/null || echo "")
        if [[ -n "$token" && "$token" != "null" ]]; then
            usedPassword="$pw"
            return 0
        fi
    done
    return 1
}

yq e '.spec.nodes.[0].inventoryReference.name = strenv(HARVESTER_INVENTORY_NODE)' -i node_manifest.yaml
yq e '.metadata.name = strenv(HARVESTER_CLUSTER_NAME)' -i node_manifest.yaml
yq e '.spec.version = strenv(HARVESTER_VERSION)' -i node_manifest.yaml
if [[ -n "${HARVESTER_IMAGE_URL:-}" && -n "${HARVESTER_IMAGE_URL//[[:space:]]/}" ]]; then
    yq e '.spec.imageURL = strenv(HARVESTER_IMAGE_URL)' -i node_manifest.yaml
fi
yq e '.spec.imageURL' node_manifest.yaml
chmod 600 "$SSH_KEY_FILE"
PUBLIC_SSH_KEY="$(ssh-keygen -f "$SSH_KEY_FILE" -y)"
export PUBLIC_SSH_KEY
yq e '.spec.clusterConfig.sshKeys.[0] = strenv(PUBLIC_SSH_KEY)' -i node_manifest.yaml

if [[ -z "$EXISTING_HARVESTER_IP" ]]; then
    export KUBECONFIG=seeder.yaml

    if [[ ! -s "$KUBECONFIG" ]]; then
        echo "FATAL: seeder.yaml is empty; the SEEDER_KUBECONFIG credential is missing or not valid base64" >&2
        exit 1
    fi
    if ! kubectl get namespace tink-system >/dev/null 2>&1; then
        echo "FATAL: cannot reach the seeder cluster; check that the SEEDER_KUBECONFIG credential is a valid base64-encoded kubeconfig" >&2
        kubectl get namespace tink-system || true
        exit 1
    fi
    if ! kubectl get -n tink-system "inventories/$HARVESTER_INVENTORY_NODE" >/dev/null 2>&1; then
        echo "FATAL: inventory $HARVESTER_INVENTORY_NODE does not exist in tink-system" >&2
        exit 1
    fi
    echo "seeder reachable; inventory $HARVESTER_INVENTORY_NODE present"

    if kubectl get "clusters.metal/$HARVESTER_CLUSTER_NAME" -n tink-system >/dev/null 2>&1; then
        echo "deleting existing clusters.metal/$HARVESTER_CLUSTER_NAME (blocks on the seeder finalizer while the node is wiped)"
    fi
    kubectl delete "clusters.metal/$HARVESTER_CLUSTER_NAME" -n tink-system --ignore-not-found --wait=true --timeout=30m
    sleep 180

    kubectl apply -f node_manifest.yaml
    wait_for "PXE address on inventories/$HARVESTER_INVENTORY_NODE" 1800 10 get_pxe_address
    wait_for "clusters.metal/$HARVESTER_CLUSTER_NAME to be clusterRunning with a cluster address" 3600 30 seeder_cluster_running
fi

echo "harvester IP address"
echo "$harvester_node_ip"

wait_for "ping response from $harvester_node_ip" 1800 5 ping -c1 -W2 "$harvester_node_ip"
wait_for "harvester kubeconfig over ssh from $harvester_node_ip" 2400 15 fetch_harvester_kubeconfig

if [[ -z "$harvester_vip" ]]; then
    wait_for "harvester VIP on $harvester_node_ip" 900 5 get_vip
fi

echo "$harvester_vip" > host.txt

sed -i "s#server: https://127.0.0.1:6443#server: https://$harvester_vip:6443#g" harvester.yaml

export KUBECONFIG=harvester.yaml

wait_for "deployment harvester-system/harvester to exist" 1800 15 deployment_exists harvester-system harvester
wait_for "deployment cattle-system/rancher to exist" 1800 15 deployment_exists cattle-system rancher
kubectl get pods -A
kubectl rollout status deployment -n harvester-system harvester --timeout=20m
kubectl rollout status deployment -n cattle-system rancher --timeout=20m

wait_for "https://$harvester_vip to be healthy" 1800 5 harvester_is_healthy

token=""
usedPassword=""
jsonOutput=""
if ! wait_for "admin login to https://$harvester_vip" 600 15 harvester_login; then
    echo "FATAL: could not log in to harvester at https://$harvester_vip with any known credential" >&2
    echo "$jsonOutput" >&2
    exit 1
fi
echo "authenticated to harvester at https://$harvester_vip"

jsonOutput=$(curl --insecure --max-time 60 -X GET -H "Accept: application/json" -H "Content-Type: application/json" -H "Authorization: Bearer $token" "https://$harvester_vip/v3/users?me=true")

userID=$(echo "$jsonOutput" | jq -cr .data[0].id)
if [[ -z "$userID" || "$userID" == "null" ]]; then
    echo "FATAL: could not resolve admin user id from harvester v3 api" >&2
    echo "$jsonOutput" >&2
    exit 1
fi

jsonData=$(jq -n --arg password "password1234" '{"newPassword" : $password}')

if [[ "$usedPassword" != "password1234" ]]; then
    echo "rotating harvester admin password to the expected value"
    curl --insecure --max-time 60 --user "$token" -X POST -H 'Accept: application/json' -H 'Content-Type: application/json' -d "$jsonData" "https://$harvester_vip/v3/users/$userID?action=setpassword"
fi

echo "$token" > login.token
