# RKE2 Node Scaling Configs

## Table of Contents
1. [Prerequisites](../README.md)
2. [Test Cases](#Test-Cases)
3. [Configurations](#Configurations)
4. [Configuration Defaults](#defaults)
5. [Logging Levels](#Logging)
6. [Back to general node scaling](../README.md)

## Test Cases
All of the test cases in this package are listed below, keep in mind that all configuration for these tests have built in defaults [Configuration Defaults](#defaults). These tests will provision a cluster if one is not provided via the rancher.ClusterName field.

### Node Scaling Test

#### Description:
The node scaling test validates that node pools can be scaled up and down. All configurations are not required if an already provisioned cluster is provided to the test.

#### Required Configurations:
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `RKE2_Scale_Control_Plane`
2. `RKE2_Scale_ETCD`
3. `RKE2_Scale_Worker`
4. `RKE2_Scale_Windows`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/nodescaling/rke2 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestNodeScalingTestSuite/TestScalingNodePools -timeout=60m -v`

## Configurations

### Existing cluster:
```yaml
rancher:
  host: <rancher-fqdn>
  adminToken: <rancher-token>
  clusterName: "<existing cluster name>"
  cleanup: true
  insecure: true
```

### Provisioning cluster
This test will create a cluster if one is not provided. See to configure a node driver OR custom cluster depending on the node scaling test [rke2 provisioning](../../provisioning/rke2/README.md)

### Cloud Credential Config
Please see an example config below using AWS as the node provider to first provision the cluster:

```yaml
rancher:
  host: ""
  adminToken: ""
  insecure: true
```

### Cluster Config
```yaml
clusterConfig:
  cni: "calico"
  provider: "aws"
  nodeProvider: "ec2"
```

### Machine Config
```yaml
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
```

## Defaults
This package contains a defaults folder which contains default test configuration data for non-sensitive fields. The goal of this data is to:
1. Reduce the number of fields the user needs to provide in the cattle_config file.
2. Reduce the amount of yaml data that needs to be stored in our pipelines.
3. Make it easier to run tests

Any data the user provides will override these defaults which are stored here: [defaults](defaults/defaults.yaml).

## Logging
This package supports several logging levels. You can set the logging levels via the cattle config and all levels above the provided level will be logged while all logs below that logging level will be omitted.

```yaml
logging:
   level: "trace" #trace, debug, info, warning, error
```

## Additional
1. If the test passes immediately without warning, try adding the `-count=1` or run `go clean -cache`. This will avoid previous results from interfering with the new test run.
