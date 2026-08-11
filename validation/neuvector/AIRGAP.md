# Airgap Cluster Prerequisites for NeuVector Tests

This document lists everything the pipeline-provisioned cluster must provide before the NeuVector hardened test suite (`TestNeuVectorHardenedTestSuite`) can run in an airgap environment.

The test itself does **not** provision clusters — it targets an existing cluster via `rancher.clusterName` (see [Architecture Decision in #803](https://github.com/rancher/tests/issues/803#user-content-architecture-decision-target-existing-cluster-2026-08-10)). All infrastructure setup is the airgap pipeline's responsibility (Slice 4, [#807](https://github.com/rancher/tests/issues/807)).

---

## 1. Private Registry Configured

The downstream cluster nodes must have an RKE2/K3s `registries.yaml` pointing at the airgap private registry. This is configured by the pipeline's "Deploy Cluster" and "Deploy Registry" stages.

**RKE2** — `/etc/rancher/rke2/registries.yaml`:

```yaml
configs:
  "{}":
    auth:
      username: registry-user
      password: <PRIVATE_REGISTRY_PASSWORD>
    tls:
      insecure_skip_verify: true
```

**K3s** — `/etc/rancher/k3s/registries.yaml` (same format).

This ensures all container runtime image pulls (system images + workload images) resolve against the private registry.

---

## 2. `system-default-registry` Rancher Setting

The Rancher management cluster must have the `system-default-registry` setting configured so the NeuVector chart install propagates it into chart values.

```bash
# Set via Rancher API or UI
kubectl patch setting system-default-registry \
  --namespace cattle-system \
  --type merge \
  -p '{"value":"<REGISTRY_HOST>:<PORT>"}'
```

The test reads this setting in `SetupSuite` (`neuvector_hardened_test.go:150`) and passes it as `DefaultRegistry` in the chart payload (`actions/charts/neuvector.go`). When non-empty, the test additionally verifies that all NeuVector pods use the registry prefix (`neuvector_hardened_test.go:198-203`).

In non-airgap environments this setting exists but is empty — no change to current behavior.

---

## 3. Mirrored NeuVector Images

The private registry must contain all NeuVector component images. The exact tag depends on the chart version (the test installs the latest from `rancher-charts`).

### Core images

| Component   | Image Repository (upstream)     | Tag Source                         | Chart Path                          |
|-------------|---------------------------------|------------------------------------|-------------------------------------|
| Controller  | `neuvector/controller`          | top-level `tag` (e.g. `5.6.0`)     | `controller.image.repository`       |
| Manager     | `neuvector/manager`             | top-level `tag`                    | `manager.image.repository`          |
| Enforcer    | `neuvector/enforcer`            | top-level `tag`                    | `enforcer.image.repository`         |
| Scanner     | `neuvector/scanner`             | `cve.scanner.image.tag` (e.g. `6`) | `cve.scanner.image.repository`      |
| Updater     | `neuvector/updater`             | `cve.updater.image.tag` (e.g. `0.0.13`) | `cve.updater.image.repository` |

### Optional images (enabled by chart config)

| Component             | Image Repository (upstream)        | When needed                                    |
|-----------------------|------------------------------------|------------------------------------------------|
| Compliance config     | `neuvector/compliance-config`      | `controller.prime.enabled: true`               |
| CVE registry adapter  | `neuvector/registry-adapter`       | `cve.adapter.enabled: true`                    |

### How to extract the exact image list for a specific chart version

```bash
# Pull the chart and inspect values for image tags
helm pull rancher-charts/neuvector --version <CHART_VERSION> --untar
grep -A2 'repository:' neuvector/values.yaml | grep 'neuvector/'
```

### Mirroring command

```bash
# Example: mirror NeuVector 5.6.0 images to a private registry
REGISTRY="<REGISTRY_HOST>:<PORT>"
TAG="5.6.0"

for img in controller manager enforcer; do
  docker pull "docker.io/neuvector/${img}:${TAG}"
  docker tag  "docker.io/neuvector/${img}:${TAG}" "${REGISTRY}/neuvector/${img}:${TAG}"
  docker push "${REGISTRY}/neuvector/${img}:${TAG}"
done

# Scanner and updater have their own tags
docker pull docker.io/neuvector/scanner:6
docker tag  docker.io/neuvector/scanner:6 "${REGISTRY}/neuvector/scanner:6"
docker push "${REGISTRY}/neuvector/scanner:6"

docker pull docker.io/neuvector/updater:0.0.13
docker tag  docker.io/neuvector/updater:0.0.13 "${REGISTRY}/neuvector/updater:0.0.13"
docker push "${REGISTRY}/neuvector/updater:0.0.13"
```

> **Note:** The Rancher-packaged chart (`rancher-charts/neuvector`) may use Rancher-mirrored image names (e.g. `rancher/mirrored-neuvector-controller`). Inspect the chart values to confirm exact repository paths for your version.

---

## 4. `rancher-charts` Repo Mirrored

The airgap Rancher must have the NeuVector chart available in its local `rancher-charts` mirror. This is part of the standard airgap Rancher setup — the Rancher airgap installation tooling mirrors the charts catalog.

The test fetches chart versions via:

```go
latestVersions, err := n.client.Catalog.GetListChartVersions(actionsCharts.NeuVectorChartName, catalog.RancherChartRepo)
```

If the chart is not in the local `rancher-charts`, this call returns an empty list and the test fails.

Verify:

```bash
# Check that the NeuVector chart is available in rancher-charts
kubectl get clusterrepo rancher-charts -o jsonpath='{.status.conditions}'
```

---

## 5. NeuVector UI Extension (optional)

The NeuVector UI extension (`neuvector-ui-ext`) runs on the **local** Rancher cluster, not the downstream cluster. In airgap, choose one:

### Option A: Pre-install the extension

If the extension is already installed, the test auto-detects it via chart status and skips the install:

```go
uiExtensionObj, err := charts.GetChartStatus(n.client, "local", ...)
if uiExtensionObj.IsAlreadyInstalled { /* skip */ }
```

No configuration needed — the test handles it.

### Option B: Internal git mirror

Point the UI plugin charts ClusterRepo at an internal mirror reachable from the Rancher server:

```yaml
neuvectorTest:
  uiPluginChartsURL: "https://internal-git.example.com/rancher/ui-plugin-charts"
  uiPluginChartsBranch: "main"
```

### Option C: Skip the extension entirely

When the Rancher server cannot reach `github.com` **and** no internal mirror is configured:

```yaml
neuvectorTest:
  skipUIExtension: true
```

The suite still installs and validates the NeuVector backend chart and reaches the manager UI through the Kubernetes service proxy, so the test remains meaningful without the Rancher UI extension.

---

## Cattle-Config for Airgap

```yaml
rancher:
  host: "https://<RANCHER_HOST>"
  adminToken: "<RANCHER_ADMIN_TOKEN>"
  clusterName: "<PIPELINE_PROVISIONED_CLUSTER_NAME>"  # targets existing cluster
  insecure: true
  cleanup: true

neuvectorTest:
  skipUIExtension: true    # Option C (adjust per your airgap setup)
  # OR:
  # uiPluginChartsURL: "https://internal-git.example.com/rancher/ui-plugin-charts"
  # uiPluginChartsBranch: "main"
```

Key differences from non-airgap config:
- `rancher.clusterName` is set (no `qaInfraAutomation.customCluster` provisioning section)
- `neuvectorTest.skipUIExtension` or `neuvectorTest.uiPluginChartsURL` is configured
- The `system-default-registry` setting is configured on Rancher by the pipeline, not by the test

---

## Verification

After the pipeline provisions the cluster and before running tests, verify:

```bash
# 1. system-default-registry is set on Rancher
kubectl get setting system-default-registry -o jsonpath='{.value}'
# Expected: <REGISTRY_HOST>:<PORT> (non-empty)

# 2. NeuVector chart is available in rancher-charts
# (verify via Rancher UI or API)

# 3. Images are present in the private registry
# (verify via registry API or docker pull from an airgap node)
```

After the test run, verify:

```bash
# 4. NeuVector pods use the private registry prefix (no ImagePullBackOff)
kubectl get pods -n cattle-neuvector-system -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.spec.containers[*].image}{"\n"}{end}'
# All images should start with <REGISTRY_HOST>:<PORT>/
```

---

## Pipeline Reference (Slice 4, #807)

The airgap pipeline (`Jenkinsfile.neuvector.airgap-rke2`) is responsible for provisioning all prerequisites listed above:

| Pipeline Stage           | Prerequisite Satisfied                     |
|--------------------------|--------------------------------------------|
| Deploy Cluster           | RKE2/K3s nodes with `registries.yaml` (#1) |
| Deploy Registry          | Private registry with mirrored images (#3) |
| Deploy Rancher           | Rancher with `system-default-registry` (#2) and `rancher-charts` mirror (#4) |
| (pre-install or config)  | NeuVector UI extension (#5, if applicable) |

The pipeline generates a cattle-config with `rancher.clusterName` targeting the provisioned cluster — no `qaInfraAutomation` provisioning section is needed.

---

## Related

- [#803](https://github.com/rancher/tests/issues/803) — Parent PRD
- [#804](https://github.com/rancher/tests/issues/804) — Slice 1: Registry propagation
- [#806](https://github.com/rancher/tests/issues/806) — Slice 3: This document
- [#807](https://github.com/rancher/tests/issues/807) — Slice 4: Airgap pipeline
- [#808](https://github.com/rancher/tests/issues/808) — Slice 5: Build-tag policy
