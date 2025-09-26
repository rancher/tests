# Snapshot

The snapshot package tests taking etcd snapshots on a downstream cluster in Rancher and then restoring that snapshot in Rancher. The following workflow is followed:

1. Provision a downstream cluster
2. Perform post-cluster provisioning checks
3. Create workloads prior to taking an etcd snapshot
4. Take an etcd snapshot on the downstream cluster
5. Depending on the test, upgrade the K8s version of the downstream cluster
6. Create workloads post taking an etcd snapshot
7. Restore the etcd snapshot on the downstream cluster
8. Validate if the workloads created post taking an etcd snapshot no longer exist

Please see below for more details for your config. Please note that the config can be in either JSON or YAML (all examples are illustrated in YAML).

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

If you plan to run the `snapshot_restore_wins_test.go`, your config must include the following:

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

## Running Tests

These tests utilize Go build tags. Due to this, see the below examples on how to run the tests:

### RKE1
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/snapshot/rke1 --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestRKE1SnapshotRestoreTestSuite/TestRKE1SnapshotRestore"` \
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/snapshot/rke1 --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestRKE1S3SnapshotRestoreTestSuite/TestRKE1S3SnapshotRestore"` \
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/snapshot/rke1 --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestRKE1SnapshotRecurringTestSuite/TestRKE1SnapshotRecurringRestores"`

### RKE2/K3s
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/snapshot/rke2k3s --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestSnapshotRestoreTestSuite/TestSnapshotRestore"` \
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/snapshot/rke2k3s --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestS3SnapshotRestoreTestSuite/TestS3SnapshotRestore"` \
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/snapshot/rke2k3s --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestSnapshotRestoreWindowsTestSuite/TestSnapshotRestoreWindows"` \
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/snapshot/rke2k3s --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestSnapshotRetentionTestSuite/TestAutomaticSnapshotRetention"` \
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/snapshot/rke2k3s --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestSnapshotRecurringTestSuite/TestSnapshotRecurringRestores"`

### IPv6
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/snapshot/ipv6 --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestSnapshotIPv6RestoreTestSuite/TestSnapshotIPv6Restore"`

### Dualstack
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/snapshot/dualstack --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestSnapshotDualstackRestoreTestSuite/TestSnapshotDualstackRestore"` \
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/snapshot/dualstack --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestSnapshotDualstackRestoreWindowsTestSuite/TestSnapshotDualstackRestoreWindows"`