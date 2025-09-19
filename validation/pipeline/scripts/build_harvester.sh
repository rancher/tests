#!/bin/bash


export inode="$EXISTING_HARVESTER_IP"
if [[ -z "$EXISTING_HARVESTER_IP" ]]; then
    export KUBECONFIG=seeder.yaml
    ./kubectl delete clusters.metal/$HARVESTER_CLUSTER_NAME -n tink-system || true
    sleep 300

    ./kubectl apply -f node_manifest.yaml

    while [[ -z "$inode" ]]; do
        export inode=$(./kubectl get -n tink-system inventories/$HARVESTER_INVENTORY_NODE -o jsonpath='{.status.pxeBootConfig.address}')
        sleep 2
    done
fi
    
echo "harvester IP address"
echo $inode


if [[ -z "$EXISTING_HARVESTER_IP" ]]; then
    sleep 230
fi

until ping -c1 -W1 $inode; do sleep 2; done
echo "able to ping node, moving on to get harvester kubeconfig"


if [[ -z "$EXISTING_HARVESTER_IP" ]]; then
    sleep 660
    until timeout 60 ssh -n -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i .ssh/$AWS_SSH_KEY_NAME rancher@$inode 'sudo cat /etc/rancher/rke2/rke2.yaml'; do sleep 5; done
    echo "ssh shows rke2.yaml is up, downloading now.."
fi


ssh -n -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i .ssh/$AWS_SSH_KEY_NAME rancher@$inode 'sudo cat /etc/rancher/rke2/rke2.yaml' > harvester.yaml
sed -i "s#server: https://127.0.0.1:6443#server: https://$inode:6443#g" harvester.yaml

export KUBECONFIG=harvester.yaml


if [[ -z "$EXISTING_HARVESTER_IP" ]]; then
    sleep 300
fi

./kubectl get pods -A
./kubectl rollout status deployment -n harvester-system harvester
./kubectl rollout status deployment -n cattle-system rancher