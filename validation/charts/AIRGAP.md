# Airgap Cluster Prerequisites for Monitoring and Alerting Tests

This document lists everything the airgap environment must provide before the monitoring and alerting test suites (`TestMonitoringTestSuite` in [monitoring_test.go](monitoring_test.go), `TestAlertingTestSuite` in [alerting_test.go](alerting_test.go)) can run in it.

The tests themselves do **not** provision clusters — they target an existing cluster via `rancher.clusterName`. All infrastructure (airgapped nodes, private registry, Rancher) must be set up beforehand, by hand or by whatever automation provisions the environment.

---

## 1. Private Registry Configured

The cluster nodes — the local Rancher cluster nodes and the registered downstream (target) cluster nodes alike — must have an RKE2/K3s `registries.yaml` pointing at the airgap private registry; the chart workloads and the test-created webhook receiver run on the target cluster. Configure this on all nodes as part of the airgap cluster setup.

**RKE2** — `/etc/rancher/rke2/registries.yaml`:

```yaml
mirrors:
  # Hostless image references (e.g. rancher/shell, the kubectl sidecar of the webhook
  # receiver) resolve to docker.io/quay.io and are served through these mirrors.
  docker.io:
    endpoint:
      - "<PRIVATE_REGISTRY_URL>"
    rewrite:
      "^(.*)": "proxycache/$1"
  quay.io:
    endpoint:
      - "<PRIVATE_REGISTRY_URL>"
    rewrite:
      "^(.*)": "quaycache/$1"
configs:
  # Keyed by the actual registry endpoint — RKE2/K3s apply auth and TLS only to
  # pulls from that exact host; there is no wildcard or "{}" key.
  "<PRIVATE_REGISTRY_URL>":
    auth:
      username: registry-user
      password: <PRIVATE_REGISTRY_PASSWORD>
    tls:
      insecure_skip_verify: true
```

**K3s** — `/etc/rancher/k3s/registries.yaml` (same format).

This ensures all container runtime image pulls (chart workloads + test-created webhook receiver pods) resolve against the private registry: `system-default-registry`-prefixed images hit `configs` auth/TLS directly, and hostless `docker.io`/`quay.io` references are rewritten into the mirror cache projects. This is exactly the layout qa-infra-automation's `airgap_rke2_registry_config` role generates; a hand-built environment should match it.

---

## 2. `system-default-registry` Rancher Setting

The Rancher management cluster must have the `system-default-registry` setting configured.

```bash
# Set via Rancher API or UI
kubectl patch setting system-default-registry \
  --namespace cattle-system \
  --type merge \
  -p '{"value":"<REGISTRY_HOST>:<PORT>"}'
```

Three consumers make this setting load-bearing for the monitoring/alerting suites:

**(a) Chart installs propagate it into chart values.** The shared chart install actions (`actions/charts/ranchermonitoring.go` `InstallRancherMonitoringChart` / `UpgradeRancherMonitoringChart`, `actions/charts/rancheralerting.go` `InstallRancherAlertingChart`) read the setting and pass it as `DefaultRegistry`, which `NewChartInstall` (`actions/charts/payloads.go`) renders as `global.cattle.systemDefaultRegistry` in the chart values. The chart's `system_default_registry` helper then prefixes **every** image with `<REGISTRY_HOST>:<PORT>/` — including entries whose upstream default is `quay.io` (e.g. the admission webhook), which therefore also resolve against the private registry once the setting is non-empty.

**(b) The webhook receiver traefik image is auto-prefixed.** The webhook receiver deployment's traefik image is pinned to `traefik:v3.7.12` (configurable via `monitoringTest.webhookReceiverImage`). When the configured image carries no registry host, the deployment helper resolves the `system-default-registry` prefix and renders `<REGISTRY_HOST>:<PORT>/traefik:v3.7.12`.

**(c) The suite asserts the prefix on the running pods.** When the setting is non-empty, the suite verifies that every container and init-container image of every pod in `cattle-monitoring-system` starts with the registry prefix (`registries.CheckNamespacedPodsForRegistryPrefix`, the strict namespace-scoped check). Images that carry no registry host at all ("rancher/foo", "nginx") fail too — a pod that silently fell back to Docker Hub fails the test loudly instead of passing with an internet pull.

In non-airgap environments this setting is empty — no prefix is applied and no assertion runs, so behavior is unchanged.

---

## 3. Mirrored Monitoring and Alerting Images

The private registry must contain all images pulled by the `rancher-monitoring`, `rancher-monitoring-crd` and `rancher-alerting-drivers` charts, plus the two test-created webhook receiver images. The exact tags depend on the chart version (the tests install the latest from `rancher-charts`).

### `rancher-monitoring` / `rancher-monitoring-crd` chart images

Example tags below are from `rancher-monitoring` `104.1.3+up57.0.3`; always re-extract for your version (see the extraction recipe at the end of this section).

| Component              | Image Repository (upstream default `docker.io/…`)                              | Example Tag  | Chart Path                                          |
|------------------------|--------------------------------------------------------------------------------|--------------|-----------------------------------------------------|
| Prometheus             | `rancher/mirrored-prometheus-prometheus`                                        | `v2.50.1`    | `prometheus.image`                                  |
| Alertmanager           | `rancher/mirrored-prometheus-alertmanager`                                      | `v0.27.0`    | `alertmanager.image`                                |
| Prometheus Operator    | `rancher/mirrored-prometheus-operator-prometheus-operator`                      | `v0.72.0`    | `prometheusOperator.image`                          |
| Config reloader        | `rancher/mirrored-prometheus-operator-prometheus-config-reloader`               | `v0.72.0`    | `prometheusOperator.prometheusConfigReloader.image` |
| Admission webhook      | `rancher/mirrored-prometheus-operator-admission-webhook` (upstream default `quay.io`) | `v0.72.0` | `prometheusOperator.admissionWebhooks.deployment.image` |
| Webhook cert patch job | `rancher/mirrored-ingress-nginx-kube-webhook-certgen`                           | `v1.4.3`     | `prometheusOperator.admissionWebhooks.patch.image`  |
| Grafana                | `rancher/mirrored-grafana-grafana`                                              | `10.4.9`     | `grafana.image` (subchart)                          |
| Grafana dashboard init | `rancher/mirrored-curlimages-curl`                                              | `7.85.0`     | `grafana.downloadDashboardsImage` (subchart)        |
| Grafana initChownData  | `rancher/mirrored-library-busybox`                                              | `1.31.1`     | `grafana.initChownData.image` (subchart)           |
| Grafana sidecar        | `rancher/mirrored-kiwigrid-k8s-sidecar`                                         | `1.26.1`     | `grafana.sidecar.image` (subchart)                  |
| kube-state-metrics     | `rancher/mirrored-kube-state-metrics-kube-state-metrics`                        | `v2.10.1`    | subchart `kube-state-metrics.image`                 |
| kube-rbac-proxy        | `rancher/mirrored-kube-rbac-proxy`                                              | `v0.14.0`/`v0.15.0` | subchart `kubeRBACProxy.image` (disabled by default) |
| Node exporter          | `rancher/mirrored-prometheus-node-exporter`                                     | `v1.7.0`     | subchart `prometheus-node-exporter.image`           |
| Upgrade hook job       | `rancher/shell`                                                                 | `v0.2.1`     | `upgrade.image`                                     |
| CRD chart job image    | `rancher/shell`                                                                 | `v0.2.1`     | `rancher-monitoring-crd` `image`                    |

Notes:

- The `rancher-monitoring-crd` chart deploys only CRDs plus a `rancher/shell` job — its single image is listed above.
- Thanos images (`rancher/mirrored-thanos-thanos`) are only pulled if a Thanos sidecar/ruler is enabled; the tests do not enable them.
- Because `global.cattle.systemDefaultRegistry` prefixes every image (see §2a), the mirror must reproduce the **repository paths** under the private registry host, e.g. `<REGISTRY_HOST>:<PORT>/rancher/mirrored-prometheus-prometheus:v2.50.1` — including for the `quay.io`-defaulted admission webhook.

### `rancher-alerting-drivers` chart images

Example tags from `rancher-alerting-drivers` `109.0.0`.

| Component                    | Image Repository (upstream default `docker.io/…`)         | Example Tag | When Needed                                              |
|------------------------------|------------------------------------------------------------|-------------|----------------------------------------------------------|
| Alerting drivers CLI         | `rancher/kuberlr-kubectl`                                  | `v7.0.3`    | Always (chart entrypoint)                                |
| SMS driver (sachet)          | `rancher/mirrored-messagebird-sachet`                      | `0.3.1`     | `sms: true` — enabled by `TestAlertingTestSuite`         |
| SMS config reloader          | `rancher/mirrored-jimmidyson-configmap-reload`             | `v0.13.1`   | With sachet                                              |
| Teams driver (prom2teams)    | `rancher/mirrored-idealista-prom2teams`                     | `4.2.1`     | `teams: true` — optional `RancherAlertingOpts` flag      |

### Test-created webhook receiver images (not part of any chart)

| Component          | Image                                             | Mirrored As                              |
|--------------------|---------------------------------------------------|------------------------------------------|
| Webhook receiver   | `traefik:v3.7.12` (pinned; override via `monitoringTest.webhookReceiverImage`) | `<REGISTRY_HOST>:<PORT>/traefik:v3.7.12` (auto-prefixed, see §2b) |
| kubectl sidecar    | value of the Rancher `shell-image` setting, used **verbatim** (`rancher/shell:…`) | as-is — see note below |

> **Note (kubectl sidecar):** the sidecar that watches the receiver's access log uses the `shell-image` setting value without any prefixing, so that value itself must resolve in the private registry. Either `shell-image` is set to an image path inside the private registry, or the node `registries.yaml` mirror config must resolve the default registry path. Verify with `kubectl get setting shell-image -o jsonpath='{.value}'` and a pull from an airgap node.

### How to extract the exact image list for a specific chart version

```bash
# Pull the charts and inspect values for image repositories
# (grafana, kube-state-metrics and prometheus-node-exporter live in subcharts)
helm pull rancher-charts/rancher-monitoring --version <CHART_VERSION> --untar
grep -r -A2 'repository:' rancher-monitoring/ | grep 'rancher/'

helm pull rancher-charts/rancher-alerting-drivers --version <CHART_VERSION> --untar
grep -r -A2 'repository:' rancher-alerting-drivers/ | grep 'rancher/'
```

### Mirroring command

```bash
# Example: mirror core monitoring + alerting images to a private registry
REGISTRY="<REGISTRY_HOST>:<PORT>"

for img in \
  "mirrored-prometheus-prometheus:v2.50.1" \
  "mirrored-prometheus-alertmanager:v0.27.0" \
  "mirrored-prometheus-operator-prometheus-operator:v0.72.0" \
  "mirrored-prometheus-operator-prometheus-config-reloader:v0.72.0" \
  "mirrored-prometheus-operator-admission-webhook:v0.72.0" \
  "mirrored-ingress-nginx-kube-webhook-certgen:v1.4.3" \
  "mirrored-grafana-grafana:10.4.9" \
  "mirrored-curlimages-curl:7.85.0" \
  "mirrored-library-busybox:1.31.1" \
  "mirrored-kiwigrid-k8s-sidecar:1.26.1" \
  "mirrored-kube-state-metrics-kube-state-metrics:v2.10.1" \
  "mirrored-prometheus-node-exporter:v1.7.0" \
  "shell:v0.2.1" \
  "kuberlr-kubectl:v7.0.3" \
  "mirrored-messagebird-sachet:0.3.1" \
  "mirrored-jimmidyson-configmap-reload:v0.13.1"; do
  docker pull "docker.io/rancher/${img}"
  docker tag  "docker.io/rancher/${img}" "${REGISTRY}/rancher/${img}"
  docker push "${REGISTRY}/rancher/${img}"
done

# Webhook receiver image (test-created, pinned tag)
docker pull docker.io/library/traefik:v3.7.12
docker tag  docker.io/library/traefik:v3.7.12 "${REGISTRY}/traefik:v3.7.12"
docker push "${REGISTRY}/traefik:v3.7.12"
```

---

## 4. `rancher-charts` Repo Mirrored

The airgap Rancher must have `rancher-monitoring`, `rancher-monitoring-crd` and `rancher-alerting-drivers` available in its local `rancher-charts` mirror. With qa-infra-automation this is provided by the `airgap_rke2_charts_mirror` role (`enable_charts_mirror: true` in `ANSIBLE_VARIABLES`): the bastion mirrors the full release branch of `git.rancher.io/charts` over git smart-HTTP and the Rancher deploy repoints the `rancher-charts` ClusterRepo at it. A trimmed or hand-copied mirror is not sufficient — a latest-version-only mirror satisfies neither requirement below.

The tests fetch chart versions via:

```go
latestMonitoringVersion, err := client.Catalog.GetLatestChartVersion(charts.RancherMonitoringName, catalog.RancherChartRepo)
latestAlertingVersion, err := a.client.Catalog.GetLatestChartVersion(charts.RancherAlertingName, catalog.RancherChartRepo)
```

If a chart is missing from the local `rancher-charts`, these calls return an empty list and the tests fail. The monitoring upgrade test additionally requires **at least two** versions of `rancher-monitoring` in the mirror (`GetListChartVersions`).

Verify:

```bash
# Check that the charts are available in rancher-charts
kubectl get clusterrepo rancher-charts -o jsonpath='{.status.conditions}'
```

---

## 5. Runner Network Position

The webhook receiver accessibility check historically runs on the **go test runner**: it picks a worker node's external IP, builds `http://<nodeIP>:<nodePort>/dashboard` and issues a GET, which requires a route from the runner into the node subnet.

With the in-cluster reachability probe, node address selection follows `monitoringTest.nodeAddressPreference` (default `[ExternalIP, InternalIP]`):

- **InternalIP selected (typical airgap)** — airgap nodes usually have no external addresses, so the check executes **inside the target cluster** via the Rancher proxy (a short-lived `shell-image` job running `curl` against the node address), following the existing `kubectl.Command` pattern. The go test runner does **not** need a route into the node subnet.
- **ExternalIP selected (non-airgap default)** — the existing runner-side check runs unchanged.

The Grafana/Prometheus/Alertmanager UI validations always go through the Rancher proxy (`ingresses.IsIngressExternallyAccessible` against `rancher.host` on service proxy paths), so they never require direct node reachability.

The in-cluster probe (and the chart-resource teardown helpers) execute through a Rancher-generated kubeconfig, whose server address comes from the `server-url` setting. `server-url` must therefore be resolvable and routable **from the runner**, not only from inside the airgap network — the qa-infra deploy defaults it to the public hostname, which in-VPC DNS resolves to the private IP for nodes and cluster agents. A `server-url` pointing at an internal-only name makes every kubeconfig-based operation time out from external runners.

---

## 6. Cattle-Config for Airgap

```yaml
rancher:
  host: "https://<RANCHER_HOST>"
  adminToken: "<RANCHER_ADMIN_TOKEN>"
  clusterName: "<EXISTING_AIRGAP_CLUSTER_NAME>"  # targets existing cluster
  insecure: true
  cleanup: true

monitoringTest:
  # Optional: override the pinned webhook receiver image (default traefik:v3.7.12).
  # webhookReceiverImage: "traefik:v3.7.12"
  # Fallback only, when traefik is not mirrored in the private registry:
  # skipWebhookReceiver: true
  # Default node address preference — InternalIP fallback matters in airgap (see §5):
  nodeAddressPreference: [ExternalIP, InternalIP]
```

Key differences from non-airgap config:

- `rancher.clusterName` is set (no `qaInfraAutomation` provisioning section)
- `monitoringTest` is optional — every key has a default; `webhookReceiverImage` only overrides the pinned tag, and `skipWebhookReceiver: true` is the escape hatch when traefik cannot be mirrored (the webhook receiver section is then skipped, losing that coverage)
- `nodeAddressPreference` keeps `[ExternalIP, InternalIP]`; in airgap the InternalIP fallback plus the in-cluster probe removes the runner-to-node-subnet requirement
- The `system-default-registry` setting is configured on Rancher beforehand, not by the test

---

## 7. External Egress Audit — Methodology

A properly airgapped environment denies external egress **by construction**: nodes live in private subnets with no direct internet route. This section is the checklist for proving that a monitoring/alerting run makes **zero** outbound attempts to the internet. During a run, confirm no outbound attempts reach:

- **registries**: `docker.io` (`registry-1.docker.io`), `quay.io` — every image must come from the private registry (§2, §3)
- **`github.com`** — chart tarballs must come from the local `rancher-charts` mirror, not upstream sources
- **Grafana asset hosts** (`grafana.com` / Grafana plugin and dashboard CDN hosts) — Grafana UI validation goes through the Rancher proxy (`ingresses.IsIngressExternallyAccessible` against `rancher.host`), so plugin/dashboard content must come from the mirrored chart, not the internet

Evidence sources:

- firewall/iptables counters at the environment's egress point, if any (e.g. a bastion) — per-destination hit counters before/after the run
- node DNS resolver logs (no lookups for the hosts above during the test window)

**Results: to be recorded from the first full airgap run; this section is the checklist.** The live audit requires access to the environment's firewall counters and node DNS logs, which usually means the run happens in a purpose-built airgap environment rather than a dev workstation. If the audit surfaces product-level Grafana behavior that needs airgap preparation (e.g. a plugin fetched from the internet), it will be documented as an additional prerequisite in §3.

---

## Environment Setup Checklist

Whether the environment is built by hand or by automation, the same prerequisites apply before the suites can run:

| Setup Step                                      | Prerequisite Satisfied                                   |
|-------------------------------------------------|----------------------------------------------------------|
| Provision the RKE2 Rancher cluster and downstream cluster | RKE2 nodes with `registries.yaml` (#1)           |
| Point all nodes at the private registry         | Private registry with mirrored images (#3)               |
| Deploy Rancher                                  | Rancher with `system-default-registry` (#2) and `rancher-charts` mirror (#4) |
| Register the downstream cluster in Rancher      | cattle-config receives `rancher.clusterName`; no provisioning section (#6) |
| Run the Go test suites                          | Runner network position per §5; egress audit per §7      |

`rancher.host`, `adminToken`, and `clusterName` come from the test configuration file (the snippet in §6); when running under automation they are typically injected into it. `monitoringTest` keys are only needed to override defaults.


