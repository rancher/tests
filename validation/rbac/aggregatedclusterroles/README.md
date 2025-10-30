# Aggregated Cluster Roles

## Pre-requisites

- Ensure you have an existing cluster that the user has access to. If you do not have a downstream cluster in Rancher, create one first before running this test.

## Test Setup

Your GO suite should be set to `-run ^Test<TestSuite>$`

- To run the CRTB tests in aggregated_cluster_roles_crtb_test.go, set the GO suite to `-run ^TestAggregatedClusterRolesCrtbTestSuite$`
- To run the PRTB tests in aggregated_cluster_roles_prtb_test.go, set the GO suite to `-run ^TestAggregatedClusterRolesPrtbTestSuite$`
- To run the CRTB tests in aggregated_cluster_roles_cleanup_test.go, set the GO suite to `-run ^TestAggregatedClusterRolesCleanupTestSuite$`

In your config file, set the following:

```yaml
rancher: 
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  insecure: True #optional
  cleanup: True #optional
  clusterName: "cluster_name"
```
