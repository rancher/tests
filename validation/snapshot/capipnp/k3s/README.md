# K3S Snapshot Configs

## Table of Contents
1. [Tests Cases](#Test-Cases)
2. [Configurations](#Configurations)
3. [Configuration Defaults](#defaults)
4. [Logging Levels](#Logging)
5. [Back to general snapshot](../README.md)

## Test Cases
All of the test cases in this package are listed below, keep in mind that all configuration for these tests have built in defaults [Configuration Defaults](#defaults). These tests will provision a cluster if one is not provided via the rancher.ClusterName field.

### Snapshot Restore Etcd Test

#### Description:
The snapshot restore test validates that snapshots can be created and restored without any failures or longterm disruption to workloads.

#### Required Configurations:
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `CAPI_PnP_K3S_Restore_ETCD`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/snapshot/capipnp/k3s --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestCAPIPnPSnapshotRestoreEtcd -timeout=1h -v`

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
This test will create a cluster if one is not provided, see the section on having a defined machine pool in this reference README: [k3s provisioning](../../../provisioning/k3s/README.md). Additionally, you will need this section for CAPI configurations:

```yaml
capipnp:
  awsCredentials:
    accessKeyID: "<required>"
    secretAccessKey: "<required>"
  awsTemplate:
    ami: "<required>"
    controlPlaneSecurityGroup: "sg-<required>"
    nodeSecurityGroup: "sg-<required>"
    region: "us-west-1"
    sshKeyName: "<required>"
    subnetId: "subnet-<required>"
    vpcId: "vpc-<required>"
  vsphereCredentials:
    username: "<required>"
    password: "<required>"
  vsphereTemplate:
    datacenter: "<required>"
    datastore: "<required>"
    diskGiB: 40
    folder: "<required>"
    host: "<required>"
    memoryMiB: 8192
    networkName: "<required>"
    numCPUs: 4
    resourcePool: "<required>"
    template: "<required>"
  clusterNamePrefix: "<required>"
  provider: "<required>"    # capa or capv
```

## Defaults
This package contains a defaults folder which contains default test configuration data for non-sensitive fields. The goal of this data is to: 
1. Reduce the number of fields the user needs to provide in the cattle_config file. 
2. Reduce the amount of yaml data that needs to be stored in our pipelines.
3. Make it easier to run tests

Any data the user provides will override these defaults which are stored here: [defaults](../defaults/defaults.yaml). 

## Logging
This package supports several logging levels. You can set the logging levels via the cattle config and all levels above the provided level will be logged while all logs below that logging level will be omitted. 

```yaml
logging:
   level: "trace" #trace debug, info, warning, error
```

## Additional
1. If the tests passes immediately without warning, try adding the `-count=1` or run `go clean -cache`. This will avoid previous results from interfering with the new test run.
2. All of the tests utilize parallelism when running for more finite control of how things are run in parallel use the -p and -parallel.