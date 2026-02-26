# NeuVector Validation Tests

Tests in this directory verify NeuVector installation on Rancher-managed downstream clusters.
Infrastructure is provisioned via the `interoperability/qainfraautomation` package.

## Prerequisites

- A running Rancher instance reachable by the test binary
- The [`rancher-qa-infra-automation`](https://github.com/rancher/rancher-qa-infra-automation) repository cloned locally
- OpenTofu installed and in `$PATH`
- Ansible installed and in `$PATH`
- AWS credentials or Harvester kubeconfig depending on the node provider used

## Config

The test reads configuration from a YAML file pointed to by `$CATTLE_TEST_CONFIG`.

Top-level key: `qaInfraAutomation`

### Rancher-provisioned cluster (used by `neuvector_hardened_test.go`)

```yaml
qaInfraAutomation:
  repoPath: /path/to/rancher-qa-infra-automation
  workspace: default          # OpenTofu workspace; defaults to "default"
  rancherCluster:
    kubernetesVersion: v1.31.4+rke2r1
    generateName: tf
    cloudProvider: harvester  # "aws", "harvester", or "linode"
    isNetworkPolicy: true
    psa: rancher-privileged
    machinePools:
      - controlPlaneRole: true
        etcdRole: true
        quantity: 1
      - workerRole: true
        quantity: 2
    nodeConfig:               # provider-prefixed keys passed to the tofu module
      harvester_cpu_count: 4
      harvester_memory_size: 6Gi
```

### Custom cluster (Ansible-registered nodes)

Set exactly one of `aws` or `harvester` alongside `customCluster`:

```yaml
qaInfraAutomation:
  repoPath: /path/to/rancher-qa-infra-automation
  workspace: default
  customCluster:
    kubernetesVersion: v1.31.4+rke2r1
    generateName: tf
    isNetworkPolicy: true
    psa: rancher-privileged
  harvester:
    kubeConfigPath: /path/to/harvester.kubeconfig
    sshPublicKey: "ssh-ed25519 AAAA..."
    sshPrivateKeyPath: /path/to/id_ed25519
    sshUser: ubuntu
    networkName: harvester-public/vlan2011
    imageId: harvester-public/noble-cloudimg-amd64
    nodes:
      - count: 1
        role: [etcd, cp]
      - count: 2
        role: [worker]
```

For AWS nodes replace the `harvester` block with:

```yaml
  aws:
    accessKey: AKIA...
    secretKey: ...
    region: us-east-2
    ami: ami-01de4781572fa1285
    sshUser: ec2-user
    sshPublicKeyPath: /path/to/id_ed25519.pub
    sshPrivateKeyPath: /path/to/id_ed25519
    instanceType: t3a.xlarge
    vpc: vpc-0123456789abcdef0
    subnet: subnet-0123456789abcdef0
    securityGroups: [sg-0123456789abcdef0]
    route53Zone: qa.rancher.space
    hostnamePrefix: tf
    nodes:
      - count: 1
        role: [etcd, cp]
      - count: 2
        role: [worker]
```

## Running

```sh
export CATTLE_TEST_CONFIG=/path/to/config.yaml

# run all tests in this directory
go test -v -tags validation ./validation/neuvector/...

# run a specific test
go test -v -tags validation ./validation/neuvector/... -run TestNeuVectorHardenedTestSuite
```

Infrastructure cleanup is registered via `t.Cleanup()` inside the provisioning helpers, so it
runs automatically when the test finishes (pass or fail).
