# RBAC Certificate Tests

## Pre-requisites
- Ensure you have an existing cluster that the user has access to. If you do not have a downstream cluster in Rancher, create one first before running this test.

## Test Overview
This test suite verifies RBAC permissions for Certificate/TLS Secret resources across different user roles:
- Cluster Owner
- Cluster Member
- Project Owner
- Project Member
- Read Only

The tests validate that each role has the appropriate permissions to perform these operations:
- Create certificate/TLS secrets
- List certificate/TLS secrets
- Update certificate/TLS secrets
- Delete certificate/TLS secrets

## Test Setup
Your GO suite should be set to `-run ^TestCertificateRBACTestSuite$`

In your config file for **TestCertificateRBACTestSuite**, set the following:

```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  insecure: True #optional
  cleanup: True #optional
  clusterName: "cluster_name"
```