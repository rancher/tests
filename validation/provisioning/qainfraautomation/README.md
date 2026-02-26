# QA Infra Automation Provisioning Tests

Tests in this package provision infrastructure via [rancher-qa-infra-automation](https://github.com/rancher/rancher-qa-infra-automation) (OpenTofu + Ansible) and validate the resulting clusters against a running Rancher instance.

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [Test Cases](#test-cases)
3. [Configuration](#configuration)
4. [Example Config](#example-config)
5. [Running the Tests](#running-the-tests)
6. [Cleanup](#cleanup)

---

## Prerequisites

The following must be installed and available on `$PATH` on the machine running the tests.

### Required tools

| Tool | Minimum version | Notes |
|------|----------------|-------|
| `tofu` | 1.8.2 | [Install OpenTofu](https://opentofu.org/docs/intro/install/) |
| `ansible-playbook` | 2.9+ | `pip install ansible` |
| `ssh-agent` | any | Must be running; the test calls `ssh-add` automatically |

### Required Ansible collection

```bash
ansible-galaxy collection install cloud.terraform
```

The `cloud.terraform.terraform_state` inventory plugin (used by the custom cluster playbook) is provided by this collection.

### Required Python packages

```bash
python3 -m pip install ansible kubernetes
```

### Required repositories

Clone the `rancher-qa-infra-automation` repository to a known path on disk. Set `qaInfraAutomation.repoPath` in your config to that path.

```bash
git clone https://github.com/rancher/rancher-qa-infra-automation /path/to/rancher-qa-infra-automation
```

### Required infrastructure

- A running **Rancher** instance (URL + admin token go in `rancher.host` / `rancher.adminToken`)
- A **Harvester** cluster accessible from the test machine, with:
  - A kubeconfig for the Harvester API
  - An SSH key pair whose public key can be injected into VMs
  - A VM image and network already created in Harvester

---

## Test Cases

### TestHarvesterCustomCluster

**Suite:** `TestCustomClusterTestSuite`

**Description:**
Provisions Harvester VMs via OpenTofu, creates a Rancher custom downstream cluster on those VMs via Ansible, waits for the cluster to become ready, then destroys all resources on cleanup.

The high-level flow is:

1. Copy the Harvester kubeconfig to `<repoPath>/tofu/harvester/modules/local.yaml`
2. `tofu apply` — `tofu/harvester/modules/vm` (creates VMs)
3. `tofu apply` — `tofu/rancher/custom_cluster` (creates cluster registration token in Rancher)
4. Render Ansible inventory from template
5. `ssh-add` the private key
6. `ansible-playbook` — `ansible/rancher/downstream/custom_cluster/custom-cluster-playbook.yml`
7. Read `cluster_name` output from tofu state
8. Fetch cluster from Rancher Steve API and verify it is ready

**Required config keys:**
- `rancher`
- `qaInfraAutomation.repoPath`
- `qaInfraAutomation.workspace`
- `qaInfraAutomation.harvester`
- `qaInfraAutomation.customCluster`

**Run command:**
```bash
gotestsum --format standard-verbose \
  --packages=github.com/rancher/tests/validation/provisioning/qainfraautomation \
  --junitfile results.xml --jsonfile results.json \
  -- -tags=validation -run TestCustomClusterTestSuite -timeout=2h -v
```

---

### TestRancherProvisionedCluster

**Suite:** `TestRancherProvisionedClusterTestSuite`

**Description:**
Provisions a downstream cluster in Rancher where Rancher itself manages node lifecycle via a cloud provider node driver (e.g. Harvester, AWS, Linode). Uses the `tofu/rancher/cluster` module from the `rancher-qa-infra-automation` repository. Verifies the cluster becomes ready, then destroys it on cleanup.

The high-level flow is:

1. Write `rancher-cluster-vars.json` tfvars to `<repoPath>/`
2. `tofu apply` — `tofu/rancher/cluster` (Rancher provisions nodes via the selected cloud driver)
3. Read `name` output from tofu state
4. Fetch cluster from Rancher Steve API and verify it is ready

**Required config keys:**
- `rancher`
- `qaInfraAutomation.repoPath`
- `qaInfraAutomation.workspace`
- `qaInfraAutomation.rancherCluster`

**Run command:**
```bash
gotestsum --format standard-verbose \
  --packages=github.com/rancher/tests/validation/provisioning/qainfraautomation \
  --junitfile results.xml --jsonfile results.json \
  -- -tags=validation -run TestRancherProvisionedClusterTestSuite -timeout=2h -v
```

---

## Configuration

All settings live under the top-level `qaInfraAutomation` key in your cattle config file.

### `qaInfraAutomation`

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `repoPath` | string | yes | Absolute path to your local clone of `rancher-qa-infra-automation` |
| `workspace` | string | no | OpenTofu workspace name for state isolation (default: `"default"`) |
| `harvester` | object | yes* | Harvester VM provisioning settings |
| `aws` | object | no | AWS EC2 node settings (used when `rancherCluster.cloudProvider` is `"aws"` and for standalone Ansible flows) |
| `customCluster` | object | yes* | Rancher custom cluster settings |
| `standaloneCluster` | object | yes* | Settings for standalone RKE2/K3S cluster tests |
| `rancherCluster` | object | yes* | Settings for Rancher-provisioned downstream cluster tests |

\* Required only for the specific test suite that uses that section.

### `qaInfraAutomation.harvester`

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `kubeConfigPath` | string | yes | Path to the Harvester kubeconfig; copied to `<repoPath>/tofu/harvester/modules/local.yaml` |
| `sshPublicKey` | string | yes | Public SSH key injected into VMs via cloud-init |
| `sshPrivateKeyPath` | string | yes | Path to the matching private key; used by Ansible via `ssh-agent` |
| `sshUser` | string | no | SSH user for the VM image (default: `"ubuntu"`) |
| `networkName` | string | yes | Harvester network (e.g. `"harvester-public/vlan2011"`) |
| `imageId` | string | yes | Harvester image ID (e.g. `"harvester-public/noble-cloudimg-amd64"`) |
| `namespace` | string | no | Harvester namespace for resources (default: `"default"`) |
| `generateName` | string | no | Name prefix for created resources (default: `"tf"`) |
| `cpu` | int | no | vCPUs per VM (default: `4`) |
| `memory` | string | no | Memory per VM (default: `"6Gi"`) |
| `diskSize` | string | no | Disk size per VM (default: `"30Gi"`) |
| `nodes` | list | yes | VM node groups — see below |

#### `qaInfraAutomation.harvester.nodes[]`

| Field | Type | Description |
|-------|------|-------------|
| `count` | int | Number of VMs in this group |
| `role` | list of string | Rancher node roles: `"etcd"`, `"cp"`, `"worker"` |

### `qaInfraAutomation.customCluster`

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `kubernetesVersion` | string | yes | Kubernetes version (e.g. `"v1.31.4+rke2r1"`) |
| `generateName` | string | no | Short prefix used to name the cluster in Rancher (default: `"tf"`) |
| `isNetworkPolicy` | bool | no | Enable network policy (default: `false`) |
| `psa` | string | no | Pod Security Admission template (e.g. `"rancher-privileged"`) |

### `qaInfraAutomation.standaloneCluster`

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `kubernetesVersion` | string | yes | Kubernetes version string passed to Ansible (e.g. `"v1.31.4+rke2r1"`) |
| `cni` | string | no | CNI plugin to install (e.g. `"canal"`, `"calico"`) |
| `channel` | string | no | Release channel (e.g. `"stable"`, `"latest"`) |
| `kubeconfigOutputPath` | string | yes | Local path where the cluster kubeconfig will be written after creation |

### `qaInfraAutomation.aws`

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `region` | string | no | AWS region to deploy into (e.g. `"us-west-1"`) |
| `sshPrivateKeyPath` | string | yes | Path to the private SSH key used by Ansible to connect to EC2 nodes |
| `generateName` | string | no | Short name prefix appended to created resources (default: `"tf"`) |

### `qaInfraAutomation.rancherCluster`

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `kubernetesVersion` | string | yes | Kubernetes version (e.g. `"v1.32.5+rke2r1"`) |
| `generateName` | string | no | Short prefix used when naming resources in Rancher (default: `"tf"`) |
| `isNetworkPolicy` | bool | no | Enable network policy (default: `false`) |
| `psa` | string | no | Pod Security Admission template (e.g. `"rancher-privileged"`) |
| `cloudProvider` | string | yes | Node driver Rancher uses to provision nodes. Supported: `"harvester"`, `"aws"`, `"linode"` |
| `machinePools` | list | no | Node pool topology — see below. Defaults to one all-roles pool with 1 node |
| `nodeConfig` | map | yes | Provider-specific node configuration passed to the `tofu/rancher/cluster` module — see below |

#### `qaInfraAutomation.rancherCluster.machinePools[]`

| Field | Type | Description |
|-------|------|-------------|
| `controlPlaneRole` | bool | Assign the control-plane role to nodes in this pool |
| `workerRole` | bool | Assign the worker role to nodes in this pool |
| `etcdRole` | bool | Assign the etcd role to nodes in this pool |
| `quantity` | int | Number of nodes in this pool (default: `1`) |

#### `qaInfraAutomation.rancherCluster.nodeConfig`

Provider-specific fields passed verbatim to the `tofu/rancher/cluster` module as the `node_config` variable. Use the provider-prefixed keys that match your chosen `cloudProvider`.

**Linode keys:**

| Key | Description |
|-----|-------------|
| `linode_token` | Linode API token |
| `linode_region` | Linode region (e.g. `"us-west"`) |
| `linode_instance_type` | Linode instance type (e.g. `"g6-standard-8"`) |
| `linode_image` | Linode image (e.g. `"linode/ubuntu22.04"`) |
| `linode_root_pass` | Root password for the Linode instance |
| `linode_ssh_user` | SSH user (e.g. `"root"`) |
| `linode_authorized_users` | Authorized Linode users |
| `linode_tags` | Comma-separated resource tags |
| `linode_create_private_ip` | Whether to create a private IP (e.g. `true`) |
| `linode_swap_size` | Swap size in MB as a string (e.g. `"512"`) |

**Harvester keys:**

| Key | Description |
|-----|-------------|
| `harvester_cluster_v1_id` | Harvester cluster ID in Rancher (e.g. `"c-m-rbx5275x"`) |
| `harvester_cluster_type` | Cluster type (e.g. `"imported"`) |
| `harvester_ssh_user` | SSH user for VMs (e.g. `"ubuntu"`) |
| `harvester_cpu_count` | vCPU count as a string (e.g. `"4"`) |
| `harvester_memory_size` | Memory in GiB as a string (e.g. `"8"`) |
| `harvester_vm_namespace` | Harvester namespace (e.g. `"default"`) |
| `harvester_disk_info` | JSON disk specification |
| `harvester_network_info` | JSON network specification |
| `harvester_user_data` | cloud-init user-data string |

**AWS keys:**

| Key | Description |
|-----|-------------|
| `aws_access_key` | AWS access key ID |
| `aws_secret_key` | AWS secret access key |
| `aws_ami` | AMI ID (e.g. `"ami-0e01311d1f112d4d0"`) |
| `aws_instance_type` | EC2 instance type (e.g. `"t3a.2xlarge"`) |
| `aws_security_group` | List of security group names |
| `aws_subnet` | Subnet ID |
| `aws_availability_zone` | Availability zone suffix (e.g. `"b"`) |
| `aws_vpc` | VPC ID |
| `aws_region` | AWS region (e.g. `"us-west-1"`) |
| `aws_volume_size` | Root volume size in GiB |
| `aws_volume_type` | Root volume type (e.g. `"gp3"`) |

---

## Example Config

Copy this to your cattle config file and fill in the `<required>` placeholders.

```yaml
# Rancher connection — required for all tests
rancher:
  host: "rancher.example.com"
  adminToken: "token-xxxxx:yyyyy"
  insecure: true
  cleanup: true

qaInfraAutomation:
  # Absolute path to your local clone of rancher-qa-infra-automation.
  repoPath: "/home/user/rancher-qa-infra-automation"

  # OpenTofu workspace name — use something unique per test run
  # to keep state isolated (e.g. your username or a CI build ID).
  workspace: "myname-test1"

  harvester:
    # Path to the Harvester cluster kubeconfig on this machine.
    kubeConfigPath: "/home/user/.kube/harvester.yaml"

    # Public SSH key content (the key itself, not the path).
    sshPublicKey: "ssh-ed25519 AAAA... user@host"

    # Path to the matching private key.
    sshPrivateKeyPath: "/home/user/.ssh/id_ed25519"

    sshUser: "ubuntu"
    networkName: "harvester-public/vlan2011"
    imageId: "harvester-public/noble-cloudimg-amd64"
    namespace: "default"
    generateName: "tf"
    cpu: 4
    memory: "6Gi"
    diskSize: "30Gi"

    # Node groups — one entry per role grouping.
    # This example creates 1 etcd+controlplane VM and 1 worker VM.
    nodes:
      - count: 1
        role: ["etcd", "cp"]
      - count: 1
        role: ["worker"]

  customCluster:
    kubernetesVersion: "v1.31.4+rke2r1"
    generateName: "tf"
    isNetworkPolicy: false
    psa: ""

  # rancherCluster — required for TestRancherProvisionedClusterTestSuite.
  # Rancher itself provisions nodes via the chosen cloud provider node driver.
  rancherCluster:
    kubernetesVersion: "v1.32.5+rke2r1"
    generateName: "tf"
    isNetworkPolicy: false
    psa: ""

    # Cloud provider node driver: "harvester", "aws", or "linode"
    cloudProvider: "linode"

    # Machine pools (omit to default to one all-roles pool with 1 node).
    machinePools:
      - controlPlaneRole: true
        etcdRole: true
        workerRole: true
        quantity: 1

    # Provider-specific node config passed to the tofu/rancher/cluster module.
    # Keys are prefixed by the cloudProvider name.
    nodeConfig:
      linode_token: "<required>"
      linode_region: "us-west"
      linode_instance_type: "g6-standard-8"
      linode_image: "linode/ubuntu22.04"
      linode_root_pass: "<required>"
      linode_ssh_user: "root"
      linode_authorized_users: ""
      linode_tags: ""
      linode_create_private_ip: true
      linode_swap_size: "512"

    # Harvester example (replace nodeConfig block above with this when cloudProvider: "harvester"):
    # nodeConfig:
    #   harvester_cluster_v1_id: "<required>"
    #   harvester_cluster_type: "imported"
    #   harvester_ssh_user: "ubuntu"
    #   harvester_cpu_count: "4"
    #   harvester_memory_size: "8"
    #   harvester_vm_namespace: "default"
    #   harvester_disk_info: |
    #     {"disks":[{"imageName":"harvester-public/noble-cloudimg-amd64","bootOrder":1,"size":30}]}
    #   harvester_network_info: |
    #     {"interfaces":[{"networkName":"harvester-public/vlan2011"}]}
    #   harvester_user_data: |
    #     #cloud-config
    #     package_update: true
    #     packages:
    #       - qemu-guest-agent
    #     runcmd:
    #       - [systemctl, enable, --now, qemu-guest-agent.service]

    # AWS example (replace nodeConfig block above with this when cloudProvider: "aws"):
    # nodeConfig:
    #   aws_access_key: "<required>"
    #   aws_secret_key: "<required>"
    #   aws_ami: "ami-0e01311d1f112d4d0"
    #   aws_instance_type: "t3a.2xlarge"
    #   aws_security_group: ["rancher-nodes"]
    #   aws_subnet: "<required>"
    #   aws_availability_zone: "b"
    #   aws_vpc: "<required>"
    #   aws_region: "us-west-1"
    #   aws_volume_size: 50
    #   aws_volume_type: "gp3"
```

---

## Running the Tests

### With `gotestsum` (recommended)

```bash
export CATTLE_CONFIG_FILE_PATH=/path/to/your/config.yaml

# Run the Harvester custom cluster suite
gotestsum --format standard-verbose \
  --packages=github.com/rancher/tests/validation/provisioning/qainfraautomation \
  --junitfile results.xml --jsonfile results.json \
  -- -tags=validation -run TestCustomClusterTestSuite -timeout=2h -v

# Run the Rancher-provisioned cluster suite
gotestsum --format standard-verbose \
  --packages=github.com/rancher/tests/validation/provisioning/qainfraautomation \
  --junitfile results.xml --jsonfile results.json \
  -- -tags=validation -run TestRancherProvisionedClusterTestSuite -timeout=2h -v
```

### With `go test`

```bash
export CATTLE_CONFIG_FILE_PATH=/path/to/your/config.yaml

# Run the Harvester custom cluster suite
go test -v -tags=validation \
  -run TestCustomClusterTestSuite \
  -timeout=2h \
  github.com/rancher/tests/validation/provisioning/qainfraautomation

# Run the Rancher-provisioned cluster suite
go test -v -tags=validation \
  -run TestRancherProvisionedClusterTestSuite \
  -timeout=2h \
  github.com/rancher/tests/validation/provisioning/qainfraautomation
```

> **Note:** The `qainfraautomation` build tag can be used as an alternative to `validation` to compile only these tests (e.g. `-tags=qainfraautomation`).

### Tips

- Pass `-count=1` or run `go clean -testcache` if a test appears to pass immediately without executing — this prevents stale cached results from interfering.
- The `-timeout` flag should account for the full `tofu apply` + Ansible playbook + cluster readiness wait. **2h is a safe default.**
- Use a unique `workspace` value per test run to avoid tofu state collisions if running multiple tests in parallel.

---

## Cleanup

Cleanup (`tofu destroy`) runs automatically via `t.Cleanup()` at the end of each test, whether the test passes or fails.

**`TestHarvesterCustomCluster` destroy order:**

1. Rancher custom cluster resources (`tofu/rancher/custom_cluster`)
2. Harvester VMs (`tofu/harvester/modules/vm`)

**`TestRancherProvisionedCluster` destroy order:**

1. Rancher-provisioned cluster resources (`tofu/rancher/cluster`)

If cleanup fails (e.g. due to a network outage), the OpenTofu workspace state is preserved. You can re-run cleanup manually:

```bash
# Custom cluster cleanup
cd /path/to/rancher-qa-infra-automation/tofu/rancher/custom_cluster
tofu workspace select <workspace>
tofu destroy -auto-approve -var-file=/path/to/rancher-custom-cluster-vars.json

cd /path/to/rancher-qa-infra-automation/tofu/harvester/modules/vm
tofu workspace select <workspace>
tofu destroy -auto-approve -var-file=/path/to/harvester-vm-vars.json

# Rancher-provisioned cluster cleanup
cd /path/to/rancher-qa-infra-automation/tofu/rancher/cluster
tofu workspace select <workspace>
tofu destroy -auto-approve -var-file=/path/to/rancher-cluster-vars.json
```

The generated `*-vars.json` files are written to the repo root (`<repoPath>/*.json`) during the test run.
