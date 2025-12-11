# K3S Snapshot Configs

## Table of Contents
1. [Prerequisites](../README.md)
2. [Tests Cases](#Test-Cases)
3. [Configurations](#Configurations)
4. [Configuration Defaults](#defaults)
5. [Logging Levels](#Logging)
6. [Back to general snapshot](../README.md)

## Test Cases
All of the test cases in this package are listed below, keep in mind that all configuration for these tests have built in defaults [Configuration Defaults](#defaults). These tests will provision a cluster if one is not provided via the rancher.ClusterName field.

### Recurring Snapshot Test

#### Description:
The recurring snapshot test verifies that a cluster can create a series of snapshots. All configurations are not required if an already provisioned cluster is provided to the test.

#### Required Configurations:
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `K3S_Recurring_Restores`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/snapshot/k3s --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestSnapshotRecurringTestSuite/TestSnapshotRecurringRestores -timeout=1h -v`

### Snapshot Restore Test

#### Description:
The snapshot restore test validates that snapshots can be created and restored without any failures or longterm disruption to workloads.

#### Required Configurations:
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `K3S_Restore_ETCD`
2. `K3S_Restore_ETCD_K8sVersion`
3. `K3S_Restore_ETCD`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/snapshot/k3s --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestSnapshotRestoreTestSuite/TestSnapshotRestore -timeout=1h -v`

### Snapshot Retention Test

#### Description:
The snapshot retention test validates that the configured number of snapshots are retained and older snapshots are deleted as expected.

#### Required Configurations:
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `K3S_Snapshot_Retention`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/snapshot/k3s --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestSnapshotRetentionTestSuite/TestSnapshotRetention -timeout=1h -v`

### Snapshot S3 Test

#### Description:
The snapshot S3 test validates that snapshots can be stored and restored from an S3 bucket.

#### Required Configurations:
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)
4. S3 configuration in etcd section of cluster config

#### Table Tests:
1. `K3S_Snapshot_S3`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/snapshot/k3s --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestSnapshotS3TestSuite/TestSnapshotS3 -timeout=1h -v`

### Dualstack Snapshot Restore Test

#### Description:
The dualstack snapshot restore test validates that a cluster configured for dualstack networking can create and restore snapshots successfully.

#### Required Configurations:
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `K3S_Dualstack_Snapshot_Restore`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/snapshot/k3s/dualstack --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestSnapshotDualstackRestoreTestSuite/TestSnapshotDualstackRestore -timeout=1h -v`

### IPv6 Snapshot Tests

#### Description:
The IPv6 snapshot tests validate snapshot creation and restore functionality on clusters configured with IPv6 networking.

#### Required Configurations:
1. [Cloud Credential](#cloud-credential-config)
2. [Cluster Config](#cluster-config) (with IPv6 settings)
3. [Machine Config](#machine-config)

#### Table Tests:
1. `K3S_IPv6_Restore_ETCD`
2. `K3S_IPv6_Restore_ETCD_K8sVersion`
3. `K3S_IPv6_Restore_Upgrade_Strategy`

#### Run Commands:
1. `gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/snapshot/k3s/ipv6 --junitfile results.xml --jsonfile results.json -- -tags=validation -run TestSnapshotIPv6RestoreTestSuite/TestSnapshotIPv6Restore -timeout=2h -v`

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
This test will create a cluster if one is not provided, see to configure a node driver OR custom cluster depending on the snapshot test [k3s provisioning](../../provisioning/k3s/README.md)

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