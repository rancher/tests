# Steve VAI Project-Scoped Secret Duplicate Project Name Tests

## Requirement

This suite requires a real downstream cluster in the cattle config. Set `rancher.clusterName` to the downstream cluster name, not `local`.

The test creates two Rancher Projects with the same custom `metadata.name`: one targeting `local` and one targeting the configured downstream cluster. It then creates project-scoped Secret fixtures in the upstream backing namespaces and verifies VAI/SQL-cache filtering, sorting, and pagination keep the two projects isolated by both project name and project cluster.

## Config

```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  insecure: True # optional
  cleanup: True # optional
  clusterName: "downstream-cluster-name"
```

## Run

Your GO suite should be set to:

```text
-run ^TestProjectScopedSecretDuplicateProjectNameTestSuite$
```

VAI/SQL cache must be enabled. The suite will enable it if needed, matching the existing VAI tests.
