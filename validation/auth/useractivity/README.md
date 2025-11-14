# UserActivity Test Suite (Public API)

This repository contains Golang automation tests for UserActivity (Public API).

## Test Setup

Your GO suite should be set to `-run ^TestUserActivityTestSuite$`. You can find specific tests by checking the test file you plan to run.

In your config file, set the following:

```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  insecure: True #optional
  cleanup: True #optional
```
