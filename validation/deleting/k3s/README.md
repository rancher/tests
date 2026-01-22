# K3S Deleting Configs

## Table of Contents
1. [Prerequisites](../README.md)
2. [Tests Cases](#Test-Cases)
3. [Configurations](#Configurations)
4. [Configuration Defaults](#defaults)
5. [Logging Levels](#Logging)
6. [Back to general deleting](../README.md)

## Test Cases
All of the test cases in this package are listed below, keep in mind that all configuration for these tests have built in defaults [Configuration Defaults](#defaults). These tests will provision a cluster if one is not provided via the rancher.ClusterName field.

### Delete cluster test

#### Description:
Verifies that a cluster can be deleted. 

#### Required Configurations:
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `K3S_Delete_Cluster`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/deleting/k3s --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestDeleteClusterTestSuite/TestDeletingCluster -timeout=1h -v`


### Delete cluster init machine test

#### Description:
Verifies that a cluster is able to recover from deleting the init machine.

#### Required Configurations:
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `K3S_Delete_Cluster`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/deleting/k3s --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestDeleteInitMachineTestSuite/TestDeleteInitMachine -timeout=1h -v`


### Delete IPv6 cluster test

#### Description:
Verifies that a ipv6 cluster can be deleted. 

#### Required Configurations:
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `K3S_Delete_IPv6_Cluster`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/deleting/k3s/ipv6 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestDeleteIPv6ClusterTestSuite/TestDeletingIPv6Cluster -timeout=1h -v`


### Delete IPv6 cluster init machine test

#### Description:
Verifies that a IPv6 cluster is able to recover from deleting the init machine.

#### Required Configurations:
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `K3S_IPv6_Delete_Init_Machine`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/deleting/k3s/ipv6 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestDeleteInitMachineIPv6TestSuite/TestDeleteInitMachineIPv6 -timeout=1h -v`


### Delete Dualstack cluster test

#### Description:
Verifies that a dualstack cluster can be deleted. 

#### Required Configurations:
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `K3S_Delete_Dualstack_Cluster`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/deleting/k3s/dualstack --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestDeleteDualstackClusterTestSuite/TestDeletingDualstackCluster -timeout=1h -v`


### Delete Dualstack cluster init machine test

#### Description:
Verifies that a dualstack cluster is able to recover from deleting the init machine.

#### Required Configurations:
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `K3S_Dualstack_Delete_Init_Machine`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/deleting/k3s/dualstack --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestDeleteInitMachineDualstackTestSuite/TestDeleteInitMachineDualstack -timeout=1h -v`

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
This test will create a cluster if one is not provided, see to configure a node driver OR custom cluster depending on the deleting test [k3s provisioning](../../provisioning/k3s/README.md)

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
   level: "trace" #trace debug, info, warning, error
```

## Additional
1. If the tests passes immediately without warning, try adding the `-count=1` or run `go clean -cache`. This will avoid previous results from interfering with the new test run.
2. All of the tests utilize parallelism when running for more finite control of how things are run in parallel use the -p and -parallel.