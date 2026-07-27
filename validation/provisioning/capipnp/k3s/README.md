# CAPI PnP Configs

## Table of Contents
1. [Prerequisites](../README.md)
2. [Tests Cases](#Test-Cases)
3. [Configurations](#Configurations)
4. [Configuration Defaults](#defaults)
5. [Logging Levels](#Logging)
6. [Back to general provisioning](../README.md)

## Test Cases
All of the test cases in this package are listed below, keep in mind that all configuration for these tests have built in defaults [Configuration Defaults](#defaults)

### Data Directories Test

#### Description: 
Data Directories test verifies that files related to k8s, systemAgent and provisioning respect the data directories feature.

#### Required Configurations: 
1. [Cluster Config](#cluster-config)
2. [Custom Cluster Config](#custom-cluster)

#### Table Tests
1. `CAPI_K3S_Split_Data_Directories`
2. `CAPI_K3S_Grouped_Data_Directories`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/capipnp/k3s --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestDataDirectories -timeout=1h -v`

### Provisioning Test

#### Description: 
CAPI PnP test verifies that a CAPI cluster can be provisioned with the desired provider such as CAPA or CAPV.

#### Required Configurations: 
1. [Cluster Config](#cluster-config)
2. [Custom Cluster Config](#custom-cluster)

#### Table Tests
1. `CAPI_K3S|etcd_cp_worker`
2. `CAPI_K3S|etcd_cp|worker`
3. `CAPI_K3S|etcd|cp|worker`
4. `CAPI_K3S|3_etcd|2_cp|3_worker`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/provisioning/capipnp/k3s --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestProvisioning -timeout=1h -v`

## Configurations

```yaml
rancher:
  host: ""
  adminToken: ""
  cleanup: true
  insecure: true
capipnp:
  awsCredentials:
    accessKeyID: ""
    secretAccessKey: ""
  awsTemplate:
    ami: ""
    controlPlaneSecurityGroup: "sg-"
    nodeSecurityGroup: "sg-"
    region: ""
    sshKeyName: ""
    subnetId: "subnet-"
    vpcId: "vpc-"
  clusterNamePrefix: ""
  provider: "capa"
```

## Logging
This package supports several logging levels. You can set the logging levels via the cattle config and all levels above the provided level will be logged while all logs below that logging level will be omitted. 

```yaml
logging:
   level: "trace" #trace debug, info, warning, error
```

## Additional
1. If the tests passes immediately without warning, try adding the `-count=1` or run `go clean -cache`. This will avoid previous results from interfering with the new test run.
2. All of the tests utilize parallelism when running for more finite control of how things are run in parallel use the -p and -parallel.