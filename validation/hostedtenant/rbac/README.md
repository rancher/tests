# Hosted Tenant Rancher RBAC Tests

Tests RBAC functionality between hosted and tenant Rancher servers, validating global role inheritance and cluster role bindings.

## Configuration

Example `cattle-config.yaml`:

```yaml
rancher:
  host: "hosted-rancher.com"
  adminToken: "token-xxxxx:hosted-token-here"
  cleanup: true
  insecure: true
  clusterName: "imported-tenant-1"

tenantRanchers:
  clients:
    - host: "tenant-rancher.com"
      adminToken: "token-xxxxx:tenant-token-here"
      cleanup: true
      insecure: true
      clusterName: "local"
```

## Prerequisites

1. Hosted and tenant Rancher instances
2. Test harness to get a working hosted tenant environment is here https://github.com/brudnak/hosted-tenant-rancher