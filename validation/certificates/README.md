# Certificate Tests

This package contains tests for certificate management and cert rotation. For the `cert_rotation_test.go` and `cert_rotation_wins_test.go` tests, the following workflow is followed:

1. Provision a downstream cluster
2. Perform post-cluster provisioning checks
3. Rotate the certificates

## Table of Contents
- [Certificate Tests](#certificate-tests)
  - [Table of Contents](#table-of-contents)
  - [Certificate Functional Tests](#certificate-functional-tests)
  - [Certificate Rotation Tests](#certificate-rotation-tests)
  - [Getting Started](#getting-started)
  - [Running the Tests](#running-the-tests)
    - [Run Certificate Functional Tests](#run-certificate-functional-tests)
    - [Run Certificate Rotation Tests](#run-certificate-rotation-tests)

## Certificate Functional Tests
The certificate functional tests validate core certificate functionality in Kubernetes clusters managed by Rancher. These tests ensure that certificates can be properly created, managed, and used with ingress resources across different namespaces and projects.

Note: RBAC tests for certificates are covered in `rbac/certificates`

## Certificate Rotation Tests
Please see an example config below using AWS as the node provider to first provision the cluster:

## Getting Started
In your config file, set the following:
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

If you running the `cert_rotation_wins_test.go`, see an example config below:

```yaml
rancher:
  host: ""
  adminToken: ""
  clusterName: ""
  insecure: true

provisioningInput:
  providers: ["vsphere"]

vmwarevsphereCredentials:
  password: ""
  username: ""
  vcenter: ""
  vcenterPort: ""

vmwarevsphereConfig:
  cfgparam: [""]
  cloneFrom: ""
  cpuCount: ""
  datacenter: ""
  datastore: ""
  datastoreCluster: ""
  diskSize: ""
  folder: ""
  hostSystem: ""
  memorySize: ""
  network: [""]
  os: ""
  password: ""
  pool: ""
  sshPassword: ""
  sshPort: ""
  sshUser: ""
  sshUserGroup: ""

vmwarevsphereMachineConfigs:
  pool: ""
  datacenter: ""
  datastore: ""
  folder: ""
  hostsystem: ""
  vmwarevsphereMachineConfig:
  - cfgparam: []
    customAttribute: []
    roles: []
    cloneFrom: ""
    cpuCount: ""
    creationType: ""
    datastoreCluster: ""
    diskSize: ""
    memorySize: ""
    network: []
    os: ""
    sshPassword: ""
    sshPort: ""
    sshUser: ""
    tag: []
    sshUserGroup: ""
  - cfgparam: []
    customAttribute: []
    roles: []
    cloneFrom: ""
    cloudConfig: ""
    contentLibrary: ""
    cpuCount: ""
    creationType: ""
    datastoreCluster: ""
    diskSize: ""
    memorySize: ""
    network: []
    os: ""
    sshPassword: ""
    sshPort: ""
    sshUser: ""
    tag: []
    sshUserGroup: ""
```

Typically, a cluster with the following 3 pools is used for testing:
```yaml
{
  {
    ControlPlane: true,
    Quantity:     1,
  },
  {
    Etcd:     true,
    Quantity: 1,
  },
  {
    Worker:   true,
    Quantity: 1,
  },
}
```

## Running the Tests
These tests utilize Go build tags. Due to this, use the commands below to run the tests:

### Run Certificate Functional Tests
Your GO suite should be set to `-run ^TestCertificateTestSuite$`

Example:
```bash
gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/certificates --junitfile results.xml -- -timeout=60m -tags=validation -v -run ^TestCertificateTestSuite$
```

### Certificate rotation

### RKE1
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/certificates/rke1 --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestRKE1CertRotationTestSuite/TestRKE1CertRotation"`

### RKE2/K3S
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/certificates/rke2k3s --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestCertRotationTestSuite/TestCertRotation"` \
`gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/certificates/rke2k3s --junitfile results.xml -- -timeout=60m -tags=validation -v -run "TestCertRotationWindowsTestSuite/TestCertRotationWindows"`

If the specified test passes immediately without warning, try adding the `-count=1` flag to get around this issue. This will avoid previous results from interfering with the new test run.
