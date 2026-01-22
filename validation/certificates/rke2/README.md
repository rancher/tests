# RKE2 Certificates Configs

## Table of Contents
1. [Prerequisites](../README.md)
2. [Tests Cases](#Test-Cases)
3. [Configurations](#Configurations)
4. [Configuration Defaults](#defaults)
5. [Logging Levels](#Logging)
6. [Back to general certificates](../README.md)

## Test Cases
All of the test cases in this package are listed below, keep in mind that all configuration for these tests have built in defaults [Configuration Defaults](#defaults). These tests will provision a cluster if one is not provided via the rancher.ClusterName field.

### IPv6 Certificate Tests

#### Description:
The IPv6 certificate test verifies that a cluster can rotate certificates.

#### Required Configurations:
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config) (with IPv6 settings)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `RKE2_IPv6_Certificate_Rotation`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/certificates/rke2/ipv6 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestCertRotationIPv6TestSuite/TestCertRotationIPv6$  -timeout=2h -v`


### Dualstack Certificate Tests

#### Description:
The Dualstack certificate test verifies that a cluster can rotate certificates.

#### Required Configurations:
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config) (with dualstack settings)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `RKE2_Dualstack_Certificate_Rotation`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/certificates/rke2/dualstack --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestCertRotationDualstackTestSuite/TestCertRotationDualstack$ -timeout=2h -v`


### Windows Certificate Tests

#### Description:
The windows certificate test verifies that a windows cluster can rotate certificates.

#### Required Configurations:
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config) (with windows worker nodes)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `RKE2_Windows_Certificate_Rotation`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/certificates/rke2 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestCertRotationWindowsTestSuite/TestCertRotationWindows$ -timeout=2h -v`


### Certificate Tests

#### Description:
The certificate test verifies that a cluster can rotate certificates.

#### Required Configurations:
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config) (with IPv6 settings)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `RKE2_Certificate_Rotation`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/certificates/rke2 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestCertRotationTestSuite/TestCertRotation$ -timeout=2h -v`

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
This test will create a cluster if one is not provided, see to configure a node driver OR custom cluster depending on the certificate test [rke2 provisioning](../../provisioning/rke2/README.md)

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