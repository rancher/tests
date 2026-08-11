# NeuVector Validation Tests

Tests in this directory verify NeuVector installation on Rancher-managed downstream clusters.
Infrastructure is provisioned via the `interoperability/qainfraautomation` package.

## Prerequisites

- A running Rancher instance reachable by the test binary
- OpenTofu installed and in `$PATH`
- Ansible installed and in `$PATH`
- provider defined in qaInfraAutomation for use in a custom cluster (custom cluster required for hardening)

## Config

The test reads configuration from a YAML file pointed to by `$CATTLE_TEST_CONFIG`.

Top-level key: `qaInfraAutomation`

### Custom cluster (Ansible-registered nodes)

Set exactly one supported provider alongside `customCluster`:

```yaml
qaInfraAutomation:
  workspace: default
  customCluster:
    kubernetesVersion: v1.34.4+rke2r1
    generateName: tf
    isNetworkPolicy: true
    psa: rancher-privileged
  <providerConfig>:
  ...
```

**Note:** Infrastructure cleanup is registered via `t.Cleanup()` inside the provisioning helpers, so it
runs automatically when the test finishes (pass or fail).

### NeuVector test options

Top-level key: `neuvectorTest`. All fields are optional; without any override the test uses the public
`https://github.com/rancher/ui-plugin-charts` repo on the `main` branch, same as before.

Point the UI plugin charts ClusterRepo at an internal git mirror (e.g. airgap):

```yaml
neuvectorTest:
  uiPluginChartsURL: "https://internal-git.example.com/rancher/ui-plugin-charts"
  uiPluginChartsBranch: "main"
  skipUIExtension: false
```

Skip the UI extension install entirely (airgap without a `ui-plugin-charts` mirror):

Use `skipUIExtension: true` when the Rancher server cannot reach `github.com` **and** no
internal mirror is configured via `uiPluginChartsURL`. In that case the ClusterRepo would
never sync and the install would fail, so the test skips it rather than attempting the install.

```yaml
neuvectorTest:
  skipUIExtension: true
```

> **Note:** This flag is **not** needed when the extension is already installed — the test
> auto-detects an existing install via chart status and skips the install on its own.
> With the extension skipped, the suite still installs and validates the NeuVector backend
> chart and reaches the manager UI through the Kubernetes service proxy, so the test remains
> meaningful without the Rancher UI extension.
