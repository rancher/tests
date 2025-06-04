# Projects

## Pre-requisites

- Ensure you have an existing cluster that the user has access to. If you do not have a downstream cluster in Rancher, create one first before running this test.

## Test Setup

Your GO suite should be set to `-run ^Test<TestSuite>$`. For example to run the projects_test.go, set the GO suite to `-run ^TestProjectsTestSuite$` You can find specific tests by checking the test file you plan to run.

In your config file for ***TestProjectScopedSecretTestSuite***, set the following:

```yaml
rancher: 
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  insecure: True #optional
  cleanup: True #optional
  clusterName: "cluster_name"
registryInput: 
  name: "registry_name"
  registryUsername: "registry_username" 
  registryPassword: "registry_password"
```

In your config file for the remaining test suites, set the following:

```yaml
rancher: 
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  insecure: True #optional
  cleanup: True #optional
  clusterName: "downstream_cluster_name"
```
