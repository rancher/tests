# Token TTL Test Suite

This package contains Golang automation tests for the general, v3 and kubeconfig token TTL default/max settings described in [rancher/rancher#45924](https://github.com/rancher/rancher/issues/45924).


## Pre-requisites

- Ensure you have access to an existing cluster.
- These tests can be run on a Rancher server without any downstream clusters.


## Test Setup

Your GO suite should be set to `-run ^Test<TestSuite>$`

- To run the general ext token suite, set the GO suite to `-run ^TestExtTokenTTLTestSuite$`
- To run the v3 (norman) token suite, set the GO suite to `-run ^TestV3TokenTTLTestSuite$`
- To run the kubeconfig token suite, set the GO suite to `-run ^TestKubeconfigTokenTTLTestSuite$`
- To run the additional checks suite, set the GO suite to `-run ^TestTokenTTLAdditionalChecksTestSuite$`

In your config file, set the following:

```yaml
rancher:
  host: "rancher_server_address"
  adminToken: "rancher_admin_token"
  insecure: True #optional
  cleanup: True #optional
```
