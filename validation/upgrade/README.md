# Upgrade

The upgrade package tests upgrading a downstream cluster to a higher specified K8s version in Rancher. The following workflow is followed:

1. Provision a downstream cluster that
2. Perform post-cluster provisioning checks
3. Upgrade the K8s version of the downstream cluster
4. Validate that the upgrade was successful

## Table of Contents
1. [Getting Started](#Getting-Started)
2. [Cloud Provider Migration](#cloud-provider-migration)

## Getting Started
Please see an example config below using AWS as the node provider to first provision the cluster:

```yaml
rancher:
  host: ""
  adminToken: ""
  insecure: true

provisioningInput:
  cni: ["calico"]
  providers: ["aws"]
  nodeProviders: ["ec2"]

clusterConfig:
  cni: "calico"
  provider: "aws"
  nodeProvider: "ec2"

awsCredentials:
  secretKey: ""
  accessKey: ""
  defaultRegion: "us-east-2"

awsMachineConfigs:
  region: "us-east-2"
  awsMachineConfig:
  - roles: ["etcd", "controlplane", "worker"]
    ami: ""
    instanceType: ""
    sshUser: ""
    vpcId: ""
    volumeType: ""
    zone: "a"
    retries: ""
    rootSize: ""
    securityGroup: [""]

amazonec2Config:
  accessKey: ""
  ami: ""
  blockDurationMinutes: "0"
  encryptEbsVolume: false
  httpEndpoint: "enabled"
  httpTokens: "optional"
  iamInstanceProfile: ""
  insecureTransport: false
  instanceType: ""
  monitoring: false
  privateAddressOnly: false
  region: "us-east-2"
  requestSpotInstance: true
  retries: ""
  rootSize: ""
  secretKey: ""
  securityGroup: [""]
  securityGroupReadonly: false
  spotPrice: ""
  sshKeyContents: ""
  sshUser: ""
  subnetId: ""
  tags: ""
  type: "amazonec2Config"
  useEbsOptimizedInstance: false
  usePrivateAddress: false
  volumeType: ""
  vpcId: ""
  zone: "a"           
```
Note: To see the `provisioningInput` in further detail, please review over the [Provisioning README](../provisioning/README.md).
See below how to run the test:

### Kubernetes Upgrade

#### RKE1
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/upgrade/rke1 --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestUpgradeRKE1KubernetesTestSuite/TestUpgradeRKE1Kubernetes"`

#### Windows
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/upgrade/rke2k3s --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestWindowsKubernetesUpgradeTestSuite/TestUpgradeWindowsKubernetes"`

#### IPv6
RKE2:
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/upgrade/rke2/ipv6 --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestUpgradeIPv6KubernetesTestSuite/TestUpgradeIPv6Kubernetes"`

K3S:
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/upgrade/k3s/ipv6 --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestUpgradeIPv6KubernetesTestSuite/TestUpgradeIPv6Kubernetes"`

#### Dualstack
RKE2:
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/upgrade/rke2/dualstack --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestUpgradeDualstackKubernetesTestSuite/TestUpgradeDualstackKubernetes"`

K3S:
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/upgrade/k3s/dualstack --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestUpgradeDualstackKubernetesTestSuite/TestUpgradeDualstackKubernetes"`

## Cloud Provider Migration
Migrates a cluster's cloud provider from in-tree to out-of-tree

### Current Support:
* AWS
  * RKE1
  * RKE2

### Pre-Requisites in the provided cluster
* in-tree provider is enabled
* out-of-tree provider is supported with your selected kubernetes version

### Running the test
```yaml
rancher:
  host: <your_host>
  adminToken: <your_token>
  insecure: true/false
  cleanup: false/true
  clusterName: "<your_cluster_name>"
```

**note** that no `upgradeInput` is required. See below how to run each of the tests:

`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/upgrade --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestCloudProviderMigrationTestSuite/TestAWS"`


## Cloud Provider Upgrade
Upgrades the chart version of cloud provider (CPI/CSI)

### Current Support:
* Vsphere
  * RKE1

### Pre-Requisites on the cluster
* cluster should have upgradeable CPI/CSI charts installed. You can do this via automation in provisioning/rke1 with the following option, chartUpgrade, which will install a version of the chart (latest - 1) that can later be upgraded to the latest version. 
```yaml
chartUpgrade:
  isUpgradable: true
```

### Running the test
```yaml
rancher:
  host: <your_host>
  adminToken: <your_token>
  insecure: true/false
  cleanup: false/true
  clusterName: "<your_cluster_name>"
vmwarevsphereCredentials:
  ...
vmwarevsphereConfig: 
  ...
```
See below how to run each of the tests:

`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/upgrade --junitfile results.xml -- -timeout=60m -tags=validation -v -run ^TestCloudProviderVersionUpgradeSuite$"`