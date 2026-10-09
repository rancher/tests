# Rancher CLI Test Suite

This repository contains Golang automation tests for both the legacy Rancher CLI `rancher/cli` and the new Rancher CLAI from `rancher/rancher-cli`.


# Pre-requisites 
The Rancher CLI/CLAI must already be installed in `PATH`. 
The tests do not download or pin it.

Ensure you have an existing cluster that the user has access to. If you do not have a downstream cluster in Rancher, create one first before running this test.

## Test Setup
Your GO suite should be set to `-run ^Test<TestSuite>$`
To run the new Rancher CLI tests in clai_test.go, set the GO suite to `-run ^TestCLAITestSuite$`
The legacy CLI tests in cli_test.go run with `-run ^TestCLITestSuite$` and need `rancherCLI: true`.

In your config file set the following:

```yaml
rancher:
  host: "rancher.example.com"
  adminToken: "token-id:token-secret"
  clusterName: "existing-downstream-cluster"
  cleanup: true
  rancherCLI: # true for legacy CLI; false for CLAI
  caCerts: |
    -----BEGIN CERTIFICATE-----
    ...
    -----END CERTIFICATE-----
```

For a self-signed or privately issued certificate, configure `rancher.caCerts` with the PEM certificate authority, or set `rancher.caFile` to a PEM file. When inline `caCerts` is used, Shepherd's `ranchercli.CLIClient` writes it to a file in its private config directory, and the suite reuses that file for its Shepherd client because Wrangler requires a CA file. The same CA file is passed to each CLI login with `--cacert`.

The admin CLI login uses `rancher.adminToken`; the standard-user login uses a token created for the test user. Each login gets its own config directory and file credential store, removed when the suite session is cleaned up.

Publicly trusted endpoints can omit both CA settings. The suite does not dynamically retrieve certificates or disable TLS verification.

For the Rancher CLAI tests, do not set `rancherCLI: true`. That option initializes Shepherd's client for the older CLI and uses its legacy login syntax.