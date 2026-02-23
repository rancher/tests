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

## Configuration

All settings live under the top-level `qaInfraAutomation` key in your cattle config file.

### `qaInfraAutomation`

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `repoPath` | string | yes | Absolute path to your local clone of `rancher-qa-infra-automation` |
| `workspace` | string | no | OpenTofu workspace name for state isolation (default: `"default"`) |
| `harvester` | object | yes* | Harvester VM provisioning settings |
| `customCluster` | object | yes* | Rancher custom cluster settings |
| `standaloneCluster` | object | no | Settings for standalone RKE2/K3S cluster tests |

*Required for `TestHarvesterCustomCluster`.

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
```

---

## Running the Tests

### With `gotestsum` (recommended)

```bash
export CATTLE_CONFIG_FILE_PATH=/path/to/your/config.yaml

gotestsum --format standard-verbose \
  --packages=github.com/rancher/tests/validation/provisioning/qainfraautomation \
  --junitfile results.xml --jsonfile results.json \
  -- -tags=validation -run TestCustomClusterTestSuite -timeout=2h -v
```

### With `go test`

```bash
export CATTLE_CONFIG_FILE_PATH=/path/to/your/config.yaml

go test -v -tags=validation \
  -run TestCustomClusterTestSuite \
  -timeout=2h \
  github.com/rancher/tests/validation/provisioning/qainfraautomation
```

### Tips

- Pass `-count=1` or run `go clean -testcache` if a test appears to pass immediately without executing — this prevents stale cached results from interfering.
- The `-timeout` flag should account for the full `tofu apply` + Ansible playbook + cluster readiness wait. **2h is a safe default.**
- Use a unique `workspace` value per test run to avoid tofu state collisions if running multiple tests in parallel.

---

## Cleanup

Cleanup (`tofu destroy`) runs automatically via `t.Cleanup()` at the end of each test, whether the test passes or fails. The destroy order is:

1. Rancher custom cluster resources (`tofu/rancher/custom_cluster`)
2. Harvester VMs (`tofu/harvester/modules/vm`)

If cleanup fails (e.g. due to a network outage), the OpenTofu workspace state is preserved. You can re-run cleanup manually:

```bash
cd /path/to/rancher-qa-infra-automation/tofu/rancher/custom_cluster
tofu workspace select <workspace>
tofu destroy -auto-approve -var-file=/path/to/rancher-custom-cluster-vars.json

cd /path/to/rancher-qa-infra-automation/tofu/harvester/modules/vm
tofu workspace select <workspace>
tofu destroy -auto-approve -var-file=/path/to/harvester-vm-vars.json
```

The generated `*-vars.json` files are written to the repo root (`<repoPath>/*.json`) during the test run.
