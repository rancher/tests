# Public API for Users Test Suite

This repository contains Golang automation tests for the Public API for Users.

## Pre-requisites

- Ensure you have access to an existing cluster.
- These tests can be run on a Rancher server without any downstream clusters.

## Test Setup

Your GO suite should be set to `-run ^TestPapiUsersTestSuite$`. You can find specific tests by checking the test file you plan to run.

In your config file, set the following:

```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  insecure: True #optional
  cleanup: True #optional
```
