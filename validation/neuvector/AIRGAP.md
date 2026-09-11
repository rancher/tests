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

## 5. NeuVector UI Extension

The NeuVector UI extension (`neuvector-ui-ext`) runs on the **local** Rancher cluster, not the downstream cluster. Its chart repo is cloned by the Rancher catalog controller over git HTTP (`ClusterRepo` `Spec.GitRepo`), so in airgap the clone target must be reachable from the `local` cluster with no `github.com` egress. Preferred order: **A** (install via bastion mirror), **B** (pre-installed), **C** (skip — fallback only).

### Option A (recommended): Bastion-hosted `ui-plugin-charts` mirror

The airgap bastion has internet access and is reachable from the airgap cluster. qa-infra-automation ships a role that mirrors the repo on the bastion and serves it over smart HTTP (rancher/tests#821 / rancher/qa-infra-automation#179):

- Role: `ansible/roles/airgap_rke2_ui_plugin_mirror`, gated by `enable_ui_plugin_mirror` (default `false` — non-NeuVector airgap runs unaffected)
- Standalone use: `make ui-plugin-mirror ENV=airgap`
- Provisioning wiring: `rke2-registry-config-playbook.yml` applies the role on the bastion when the gate is on, so the NeuVector CI job enables it purely via `ANSIBLE_VARIABLES` (`enable_ui_plugin_mirror: true`) — no pipeline edit
- Served URL: `http://<bastion-private-ip>:8080/ui-plugin-charts.git` (port and paths are role variables; the published host defaults to the bastion's primary private IPv4, since airgap nodes have no public-DNS route)

> **Why smart HTTP:** Rancher's catalog controller clones ClusterRepos with `git clone --depth=1` (shallow). Dumb HTTP (`git update-server-info` + a static file server) cannot serve shallow clones — it fails with `dumb http transport does not support shallow capabilities`, visible in the Rancher UI as a "Head failure" on the ClusterRepo. The role therefore serves smart HTTP via Apache + `git-http-backend`.

Point the test at the mirror:

```yaml
neuvectorTest:
  uiPluginChartsURL: "http://<bastion-private-ip>:8080/ui-plugin-charts.git"
  uiPluginChartsBranch: "main"
```

Verify the mirror exactly the way the catalog consumes it (from an airgap node or the Rancher server pod):

```bash
git clone --depth=1 -n -b main http://<bastion-private-ip>:8080/ui-plugin-charts.git
curl -o /dev/null -w '%{http_code}\n' \
  'http://<bastion-private-ip>:8080/ui-plugin-charts.git/info/refs?service=git-upload-pack'   # expect 200
```

> **Stale ClusterRepo:** the test creates `rancher-ui-plugins` only when it does not already exist; it never rewrites an existing repo's URL. If an earlier run left one pointing at `github.com` or an old mirror address, delete it before re-running:
> `kubectl delete clusterrepo.catalog.cattle.io rancher-ui-plugins`

### Option B: Pre-install the extension

If the extension is already installed, the test auto-detects it via chart status and skips the install:

```go
uiExtensionObj, err := charts.GetChartStatus(n.client, "local", ...)
if uiExtensionObj.IsAlreadyInstalled { /* skip */ }
```

No configuration needed — the test handles it.

### Option C: Skip the extension entirely (fallback)

When the Rancher server cannot reach `github.com` **and** no mirror is configured:

```yaml
neuvectorTest:
  skipUIExtension: true
```

The suite still installs and validates the NeuVector backend chart and reaches the manager UI through the Kubernetes service proxy, so the test remains meaningful without the Rancher UI extension — but extension coverage is lost, which is why Options A/B are preferred.

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
  # Recommended: bastion-hosted mirror (Option A) — the extension is installed and validated.
  uiPluginChartsURL: "http://<bastion-private-ip>:8080/ui-plugin-charts.git"
  uiPluginChartsBranch: "main"
  # Fallback only, when no mirror is configured (Option C): the extension is skipped.
  # skipUIExtension: true

Key differences from non-airgap config:
- `rancher.clusterName` is set (no `qaInfraAutomation.customCluster` provisioning section)
- `neuvectorTest.uiPluginChartsURL` points at the bastion `ui-plugin-charts` mirror (Option A), or `skipUIExtension: true` as the no-mirror fallback (Option C)
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

Tests run through the **generic** airgap pipeline `validation/pipeline/Jenkinsfile.airgap-rke2-tests`, reused — not copied — by the dedicated Jenkins job `airgap-rke2-neuvector-tests-pipeline` (JJB definition `qa-airgap-rke2-neuvector-tests.yml` in [`rancherlabs/jenkins-job-builder`](https://github.com/rancherlabs/jenkins-job-builder)). The job passes NeuVector-defaulted parameters (`GO_TEST_PACKAGE=./validation/neuvector/...`, `GO_TEST_CASE=-run TestNeuVectorHardenedTestSuite`) and enables the bastion mirror via `ANSIBLE_VARIABLES` (`enable_ui_plugin_mirror: true`). Set `neuvectorTest.uiPluginChartsURL` in the job's `CATTLE_TEST_CONFIG` to the bastion mirror URL for your environment.

| Pipeline Stage           | Prerequisite Satisfied                     |
|--------------------------|--------------------------------------------|
| Deploy Cluster           | RKE2/K3s nodes with `registries.yaml` (#1) |
| Deploy Registry (+ gated `airgap_rke2_ui_plugin_mirror` role) | Private registry with mirrored images (#3); bastion `ui-plugin-charts` mirror (#5, Option A) |
| Deploy Rancher           | Rancher with `system-default-registry` (#2) and `rancher-charts` mirror (#4) |
| Register downstream cluster (`add-downstream-cluster.yml`) | cattle-config receives `rancher.clusterName`; no `qaInfraAutomation` provisioning section |

In CI, `rancher.host`/`adminToken`/`clusterName` are injected by the pipeline; the mirror URL is environment-specific and belongs in `CATTLE_TEST_CONFIG`. For manual runs, `make ui-plugin-mirror ENV=airgap` stands up the mirror independently.

---

## Related

- [#803](https://github.com/rancher/tests/issues/803) — Parent PRD
- [#804](https://github.com/rancher/tests/issues/804) — Slice 1: Registry propagation
- [#806](https://github.com/rancher/tests/issues/806) — Slice 3: This document
- [#807](https://github.com/rancher/tests/issues/807) — Slice 4: Airgap pipeline
- [#808](https://github.com/rancher/tests/issues/808) — Slice 5: Build-tag policy
- [#821](https://github.com/rancher/tests/issues/821) — Slice 6: Bastion ui-plugin-charts mirror (Option A recipe)
- [rancher/qa-infra-automation#179](https://github.com/rancher/qa-infra-automation/issues/179) — `airgap_rke2_ui_plugin_mirror` role + `make ui-plugin-mirror`
