# Steve / Vai

## Pre-requisites

## Test Setup

Your GO suite should be set to `-run ^TestVaiTestSuite$`.

VAI is always enabled on Rancher 2.16+, so we skip the disabled and flag description tests.
Older versions still test both enabled and disabled.

In your config file, set the following:

```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  insecure: True # optional
  cleanup: True # optional
  clusterName: "local" # can just be checked against local
```
