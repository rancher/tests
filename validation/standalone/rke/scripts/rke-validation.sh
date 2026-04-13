#!/usr/bin/env bash
set -euo pipefail

RKE_VERSION="$1"
PEM_FILE="$2"
REGISTRY="$3"
REGISTRY_USER="$4"
REGISTRY_PASSWORD="$5"
PUBLIC_IP="$6"
PRIVATE_IP="$7"
KUBECTL_VERSION="${8:-v1.33.1}"
RESULTS=()

SSH_USER="ubuntu"
SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i $PEM_FILE -o LogLevel=ERROR"

echo "======================================="
echo "RKE Post-Release Checks"
echo "RKE Version: $RKE_VERSION"
echo "======================================="
echo

echo "Waiting for SSH..."
until ssh $SSH_OPTS $SSH_USER@$PUBLIC_IP "echo ready" &>/dev/null; do
  sleep 5
done

echo "Connected."
echo

echo "Copying SSH key..."
scp -q $SSH_OPTS "$PEM_FILE" $SSH_USER@$PUBLIC_IP:/home/$SSH_USER/node.pem
echo "Successfully copied SSH key."
echo

echo "Preparing node..."
echo

ssh $SSH_OPTS $SSH_USER@$PUBLIC_IP env KUBECTL_VERSION="$KUBECTL_VERSION" bash -s <<'REMOTE'
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive
export APT_LISTCHANGES_FRONTEND=none
export NEEDRESTART_MODE=a
export NEEDRESTART_SUSPEND=1

WORKDIR=~/rke-test
mkdir -p $WORKDIR
cd $WORKDIR

echo "Waiting for apt locks..."
while sudo fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1; do
  sleep 5
done
echo "Apt locks released."
echo

sudo dpkg --configure -a >/dev/null || true

echo "Removing old container runtimes..."
sudo apt-get remove -y docker docker-engine docker.io containerd runc -qq >/dev/null 2>&1 || true
sudo apt-get purge -y docker docker-engine docker.io containerd runc -qq >/dev/null 2>&1 || true
sudo apt-get autoremove -y -qq >/dev/null 2>&1
echo "Finished removing old container runtimes."
echo

echo "Installing dependencies..."

sudo -E apt-get update -qq >/dev/null

sudo -E apt-get install -y -qq ca-certificates curl gnupg jq >/dev/null 2>&1

echo
echo "Installing Docker..."

sudo install -m 0755 -d /etc/apt/keyrings

curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
 | sudo gpg --batch --yes --dearmor -o /etc/apt/keyrings/docker.gpg >/dev/null

echo \
"deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo $VERSION_CODENAME) stable" \
| sudo tee /etc/apt/sources.list.d/docker.list >/dev/null

sudo -E apt-get update -qq >/dev/null

DOCKER_VERSION=$(apt-cache madison docker-ce | grep -E '28\.3\.[0-9]+' | sort -V | tail -n1 | awk '{print $3}')
CLI_VERSION=$(apt-cache madison docker-ce-cli | grep -E '28\.3\.[0-9]+' | sort -V | tail -n1 | awk '{print $3}')

sudo apt-get install -y -qq --allow-downgrades \
  docker-ce=$DOCKER_VERSION \
  docker-ce-cli=$CLI_VERSION \
  containerd.io \
  docker-buildx-plugin \
  docker-compose-plugin >/dev/null 2>&1

sudo apt-mark hold docker-ce docker-ce-cli >/dev/null 2>&1

sudo systemctl enable docker >/dev/null 2>&1
sudo systemctl start docker >/dev/null 2>&1

echo "Successfully installed Docker."

docker --version
echo

echo "Installing kubectl..."

curl -fsSL https://dl.k8s.io/release/$KUBECTL_VERSION/bin/linux/amd64/kubectl -o kubectl
curl -fsSL https://dl.k8s.io/release/$KUBECTL_VERSION/bin/linux/amd64/kubectl.sha256 -o kubectl.sha256
awk '{print $1 "  kubectl"}' kubectl.sha256 > kubectl.sha256.check
test -s kubectl.sha256.check
sha256sum -c kubectl.sha256.check | grep -q ': OK$'
chmod +x kubectl
sudo mv kubectl /usr/local/bin/
rm -f kubectl.sha256 kubectl.sha256.check

echo "Successfully installed kubectl."
echo

echo "Node ready."
echo
REMOTE

echo "Installing RKE $RKE_VERSION..."

ssh $SSH_OPTS $SSH_USER@$PUBLIC_IP bash -s <<REMOTE
set -euo pipefail
cd ~/rke-test

curl -fsSL https://github.com/rancher/rke/releases/download/${RKE_VERSION}/rke_linux-amd64 -o rke
chmod +x rke
sudo mv rke /usr/local/bin/rke

echo "Successfully installed Rancher Kubernetes Engine."
echo
rke --version | head -n1
REMOTE

echo
echo "Fetching supported Kubernetes versions..."

VERSIONS=$(ssh $SSH_OPTS $SSH_USER@$PUBLIC_IP "rke config --list-version --all")

SUPPORTED=$(echo "$VERSIONS" \
  | grep '^v' \
  | grep -v deprecated \
  | sort -V)

echo
echo "Versions to test:"
echo "$SUPPORTED"

for VERSION in $SUPPORTED
do
  START_TIME=$(date +%s)

  echo
  echo "======================================="
  echo "Testing Kubernetes $VERSION"
  echo "======================================="

  ssh $SSH_OPTS $SSH_USER@$PUBLIC_IP bash -s <<'REMOTE'
set -euo pipefail
rm -rf ~/rke-test/kube_config_cluster.yml ~/rke-test/cluster.rkestate
REMOTE

  ssh $SSH_OPTS $SSH_USER@$PUBLIC_IP \
  REGISTRY="$REGISTRY" \
  REGISTRY_USER="$REGISTRY_USER" \
  REGISTRY_PASSWORD="$REGISTRY_PASSWORD" \
  VERSION="$VERSION" \
  PUBLIC_IP="$PUBLIC_IP" \
  PRIVATE_IP="$PRIVATE_IP" \
  bash -s <<'REMOTE'
set -euo pipefail
cd ~/rke-test

rm -f cluster.yml cluster.rkestate kube_config_cluster.yml

cat > cluster.yml <<EOF
nodes:
- address: "$PUBLIC_IP"
  internal_address: "$PRIVATE_IP"
  user: "ubuntu"
  ssh_key_path: "/home/ubuntu/node.pem"
  role:
  - "controlplane"
  - "etcd"
  - "worker"

enable_cri_dockerd: true

kubernetes_version: "$VERSION"

private_registries:
- url: "$REGISTRY"
  user: "$REGISTRY_USER"
  password: "$REGISTRY_PASSWORD"
  is_default: true
EOF
REMOTE

  echo
  echo "Generated cluster.yml for $VERSION"

  for attempt in {1..3}; do
    echo "Provisioning Kubernetes cluster (attempt $attempt)..."
    if ssh $SSH_OPTS $SSH_USER@$PUBLIC_IP "cd ~/rke-test && rke up --config cluster.yml >/dev/null"; then
      break
    fi
    echo "rke up failed, retrying..."
    sleep 10
    if [[ $attempt -eq 3 ]]; then
      echo "FAILED: rke up failed 3 times for $VERSION"
      exit 1
    fi
  done

  echo
  echo "Waiting for node ready..."

  NODE_INFO=$(ssh $SSH_OPTS $SSH_USER@$PUBLIC_IP bash -s <<'REMOTE'
set -euo pipefail
export KUBECONFIG=~/rke-test/kube_config_cluster.yml

for i in {1..180}; do
  READY=$(kubectl get nodes -o jsonpath='{.items[0].status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "NotReady")
  NODEVER=$(kubectl get nodes -o jsonpath='{.items[0].status.nodeInfo.kubeletVersion}' 2>/dev/null || echo "unknown")

  if [[ "$READY" == "True" ]]; then
    echo "READY::$NODEVER"
    exit 0
  fi

  echo "Node not ready yet ($i/180)..."
  sleep 5
done

echo "FAILED::timeout"
exit 1
REMOTE
)

  if [[ "$NODE_INFO" == READY::* ]]; then
    NODEVER="${NODE_INFO#READY::}"
    echo "Node is ready. Version: $NODEVER"
  else
    echo "FAILED: Node never became ready for $VERSION"
    exit 1
  fi

  NODEVER_BASE=${NODEVER%%-*}
  EXPECTED_BASE=${VERSION%%-*}

  if [[ "$NODEVER_BASE" != "$EXPECTED_BASE" ]]; then
    echo "FAILED: Version mismatch for $VERSION (node: $NODEVER_BASE, expected: $EXPECTED_BASE)"
    exit 1
  fi

  echo "PASSED: $VERSION"

  ssh $SSH_OPTS $SSH_USER@$PUBLIC_IP bash -s <<'REMOTE'
set -euo pipefail
cd ~/rke-test
rke remove --config cluster.yml --force >/dev/null || true

echo "Waiting for cluster teardown..."
for j in {1..12}; do
  NODES=$(kubectl get nodes --no-headers 2>/dev/null | wc -l || echo 0)
  if [[ "$NODES" -eq 0 ]]; then
    echo "Cluster resources successfully removed."
    break
  fi
  echo "Cluster still tearing down ($j/12)..."
  sleep 5
done
REMOTE

  END_TIME=$(date +%s)
  DURATION=$((END_TIME - START_TIME))
  RESULTS+=("$(printf '%-22s PASS   %ss' "$VERSION" "$DURATION")")
done

echo
echo "======================================="
echo "All Kubernetes versions passed"
echo "======================================="
echo
echo "Test Results:"
echo
printf '%-22s %-6s %s\n' "VERSION" "RESULT" "TIME"
printf '%-22s %-6s %s\n' "-------" "------" "----"

for RESULT in "${RESULTS[@]}"; do
  echo "$RESULT"
done