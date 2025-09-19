# Node Scaling

The nodescaling package tests scaling nodes, whether it is replacing nodes or adding and removing nodes, in downstream clusters in Rancher. For the node driver/custom cluster scaling tests, the following workflow is followed:

1. Provision a downstream cluster
2. Perform post-cluster provisioning checks
3. Scale specified node/machine role up and down

For the replacing tests, the following workflow is followed:

1. Provision a downstream cluster
2. Perform post-cluster provisioning checks
3. Replace specified node/machine

## Table of Contents
1. [Getting Started](#Getting-Started)
2. [Replacing Nodes](#Replacing-Nodes)
3. [Scaling Existing Node Pools](#Scaling-Existing-Node-Pools)
4. [Auto Replacing NOdes](#Auto-Replacing-Nodes)

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

If you plan to run the custom cluster tests, your config must include the following:

```yaml
awsEC2Configs:
  region: "us-east-2"
  awsSecretAccessKey: ""
  awsAccessKeyID: ""
  awsEC2Config:
    - instanceType: ""
      awsRegionAZ: ""
      awsAMI: ""
      awsSecurityGroups: [""]
      awsSSHKeyName: ""
      awsCICDInstanceTag: ""
      awsIAMProfile: ""
      awsCICDInstanceTag: ""
      awsUser: ""
      volumeSize: 
      roles: ["etcd", "controlplane", "worker"]
    - instanceType: ""
      awsRegionAZ: ""
      awsAMI: ""
      awsSecurityGroups: [""]
      awsSSHKeyName: ""
      awsCICDInstanceTag: ""
      awsUser: "Administrator"
      volumeSize: 
      roles: ["windows"]
sshPath: 
  sshPath: "/<path to .ssh folder>"
```

## Replacing Nodes
These tests utilize Go build tags. Due to this, see the below examples on how to run the tests:

### RKE1
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/nodescaling/rke1 --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestRKE1NodeReplacingTestSuite/TestReplacingRKE1Nodes"`

### RKE2 | K3S
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/nodescaling/rke2k3s --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestNodeReplacingTestSuite/TestReplacingNodes"`

## Scaling Existing Node Pools
Similar to the `provisioning` tests, the node scaling tests have static test cases as well as dynamicInput tests you can specify. In order to run the dynamicInput tests, you will need to define the `scalingInput` block in your config file. This block defines the quantity you would like the pool to be scaled up/down to. See an example below that accounts for node drivers, custom clusters and hosted clusters:

```yaml
provisioningInput:        # Optional block, only use if using vsphere
  providers: [""]         # Specify to vsphere if you have a Windows node in your cluster
scalingInput:
  nodeProvider: "ec2"
  nodePools:
    nodeRoles:
      worker: true
      quantity: 2
  machinePools:
    nodeRoles:
      etcd: true
      quantity: 1
  aksNodePool:
    nodeCount: 3
  eksNodePool:
    desiredSize: 6
  gkeNodePool:
    initialNodeCount: 3
```
NOTE: When scaling AKS and EKS, you will need to make sure that the `maxCount` and `maxSize` parameter is greater than the desired scale amount, respectively. For example, if you wish to have 6 total EKS nodes, then the `maxSize` parameter needs to be at least 7. This is not a limitation of the automation, but rather how EKS specifically handles nodegroups.

Additionally, for AKS, you must have `enableAutoScaling` set to true if you specify `maxCount` and `minCount`.

These tests utilize Go build tags. Due to this, see the below examples on how to run the tests:

### RKE1
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/nodescaling/rke1 --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestRKE1NodeScalingTestSuite/TestScalingRKE1NodePools"` \
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/nodescaling/rke1 --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestRKE1NodeScalingTestSuite/TestScalingRKE1NodePoolsDynamicInput"` \
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/nodescaling/rke1 --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestRKE1CustomClusterNodeScalingTestSuite/TestScalingRKE1CustomClusterNodes"` \
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/nodescaling/rke1 --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestRKE1CustomClusterNodeScalingTestSuite/TestScalingRKE1CustomClusterNodesDynamicInput"`

### RKE2 | K3S
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/nodescaling/rke2k3s --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestNodeScalingTestSuite/TestScalingNodePools"` \
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/nodescaling/rke2k3s --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestCustomClusterNodeScalingTestSuite/TestScalingCustomClusterNodes"`

### IPv6
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/nodescaling/ipv6 --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestCustomIPv6ClusterNodeScalingTestSuite/TestScalingCustomIPv6ClusterNodes"`

### AKS
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/nodescaling/hosted --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestAKSNodeScalingTestSuite/TestScalingAKSNodePools"` \
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/nodescaling/hosted --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestAKSNodeScalingTestSuite/TestScalingAKSNodePoolsDynamicInput"`

### EKS
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/nodescaling/hosted --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestEKSNodeScalingTestSuite/TestScalingEKSNodePools"` \
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/nodescaling/hosted --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestEKSNodeScalingTestSuite/TestScalingEKSNodePoolsDynamicInput"`

### GKE
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/nodescaling/hosted --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestGKENodeScalingTestSuite/TestScalingGKENodePools"` \
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/nodescaling/hosted --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestGKENodeScalingTestSuite/TestScalingGKENodePoolsDynamicInput"`

If the specified test passes immediately without warning, try adding the `-count=1` flag to get around this issue. This will avoid previous results from interfering with the new test run.


## Auto Replacing Nodes
If UnhealthyNodeTimeout is set on your machinepools, auto_replace_test.go will replace a single node with the given role. There are static tests for Etcd, ControlPlane and Worker roles.

If UnhealthyNodeTimeout is not set, the test(s) in this suite will wait for the cluster upgrade default timeout to be reached (30 mins), expecting an error on the node to remain as a negative test. 

Each test requires 2 or more nodes in the specified role's pool. i.e. if you're running the entire suite, you would need 3etcd, 2controlplane, 2worker, minimum. 

### RKE2 | K3S
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/nodescaling --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestEtcdAutoReplaceRKE2K3S/TestEtcdAutoReplaceRKE2K3S"`