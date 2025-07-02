# Deleting

The deleting package tests deleting downstream clusters in Rancher. For the `delete_cluster_test.go` test, the following workflow is followed:

1. Provision a downstream cluster
2. Perform post-cluster provisioning checks
3. Delete the cluster
4. Perform post delete cluster checks

For the `delete_init_machine_test.go` test, the following workflow is followed:

1. Provision a downstream cluster
2. Perform post-cluster provisioning checks
3. Delete the init machine in the downstream cluster

## Table of Contents
1. [Getting Started](#Getting-Started)
2. [Running Tests](#Running-Tests)

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

## Running Tests

These tests utilize Go build tags. Due to this, see the below examples on how to run the tests:

### RKE1
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/deleting/rke1 --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestDeleteRKE1ClusterTestSuite/TestDeletingRKE1Cluster"`

### RKE2/K3S
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/deleting/rke2k3s --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestDeleteClusterTestSuite/TestDeletingCluster"`

### Delete Init Machine
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/deleting/rke2k3s --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestDeleteInitMachineTestSuite/TestDeleteInitMachine"`

If the specified test passes immediately without warning, try adding the `-count=1` flag to get around this issue. This will avoid previous results from interfering with the new test run.