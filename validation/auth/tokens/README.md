# Tokens Test Suite

This repository contains Golang automation tests for tokens and Ext Tokens (Public API).

## Pre-requisites

- Ensure you have an existing cluster that the user has access to. If you do not have a downstream cluster in Rancher, create one first before running this test.

## Test Setup

Your GO suite should be set to `-run ^Test<TestSuite>$`

- To run the token_test.go, set the GO suite to `-run ^TestTokenTestSuite$`
- To run the ext_token_test.go, set the GO suite to `-run ^TestExtTokenTestSuite$`

In your config file, set the following:

```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  insecure: True #optional
  cleanup: True #optional
  clusterName: "downstream_cluster_name"
```
