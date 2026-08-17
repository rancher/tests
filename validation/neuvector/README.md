# NeuVector Validation Tests

Tests in this directory verify NeuVector installation on Rancher-managed downstream clusters.
The suite targets an existing downstream cluster when `rancher.clusterName` is set in the config
(the airgap pipeline injects this key); it provisions a hardened custom cluster via the
`interoperability/qainfraautomation` package only when `rancher.clusterName` is unset.

## Prerequisites

- A running Rancher instance reachable by the test binary
- OpenTofu installed and in `$PATH` *(provisioning path only)*
- Ansible installed and in `$PATH` *(provisioning path only)*
- provider defined in qaInfraAutomation for use in a custom cluster *(provisioning path only; custom cluster required for hardening)*

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

## Airgap environments

One config-driven test binary serves both the airgap and non-airgap schedules; there is **no**
separate airgap variant file (`*_airgap_test.go`). This is the decided policy (Option B, #808).
The `airgap.daily` tag appears only on the CI job's `go test -tags` flag to gate schedule
membership — it is not added to the test file. Environment differences live entirely in
`cattle-config.yaml`.

Invocation:

- Non-airgap: `go test -tags=validation -run TestNeuVectorHardenedTestSuite ./validation/neuvector/...`
- Airgap: `go test -tags="validation,airgap.daily" -run TestNeuVectorHardenedTestSuite ./validation/neuvector/...`

Config-difference table:

| Concern | Non-airgap config | Airgap config |
|---------|-------------------|---------------|
| Cluster | `rancher.clusterName: <cluster>` (or unset to provision) | `rancher.clusterName: <airgap-cluster>` (injected by `Jenkinsfile.airgap-rke2-tests`) |
| Registry | `system-default-registry` setting empty (upstream) | `system-default-registry` set to the private registry (set by the airgap Rancher install) |
| UI extension | defaults to `https://github.com/rancher/ui-plugin-charts` | `neuvectorTest.skipUIExtension: true`, or `neuvectorTest.uiPluginChartsURL` pointing at an internal mirror |

**Decision record for the charts suite:** the `!airgap.daily` exclusion on
`validation/charts/neuvector_test.go` (added in #770) was removed in this slice because registry
propagation (Slice 1, #804) plus the service-proxy-based UI check make the standard chart install
airgap-safe; both NeuVector suites now compile into airgap runs.
