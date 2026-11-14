# Rancher Upgrade

The `rancherupgrade` package validates upgrading the Rancher server from the previous
release line to the latest (currently 2.14.x -> 2.15.x) with interoperability products
installed. The line pair is the pipeline's default, not a suite constraint: the suite
itself is version-agnostic and gates on `rancherUpgradeInput.targetVersion`, while the
pipeline resolves the source via `RANCHER_VERSION_FROM` (empty = latest 2.14.x patch via
`SOURCE_LINE=2.14`) and the target via `RANCHER_VERSION_TO_UPGRADE` (empty = latest
released final). The first vertical is Longhorn: a pre-upgrade
phase installs the Longhorn chart and seeds a checksum workload on a Longhorn volume,
the Rancher server is then upgraded out-of-band (qa-infra-automation `make rancher-upgrade`),
and post-upgrade phases verify the server version, the surviving Longhorn installation,
the workload data, and a Longhorn chart upgrade to the version offered by the upgraded
Rancher's catalog.

Each phase is a suite test method selected via the `-run` regex and runs as its own
`go test` invocation in its own pipeline stage. All cross-phase state lives in-cluster
under deterministic names (PVC/deployment `rancher-upgrade-longhorn`, data file
`/auto-mnt/checksum-data`), never in process memory.

## Deliberate deviations

Do not "fix" the following; both are required by the upgrade workflow (rancher/tests#967):

- `TearDownSuite` does not call `session.Cleanup()` and the helpers register no cleanup
  funcs: pre-phase resources (Longhorn chart, PVC, workload) must survive into the
  later phases, which run as separate processes. The pipeline environment teardown is
  the cleanup.
- The suite is admin-only: upgrade phases assert server-level state and the pipeline
  runs them with the admin token.
- The server upgrade stays out-of-band (a pipeline stage running qa-infra-automation
  `make rancher-upgrade`) rather than a step inside the test: the go test phases
  assert outcomes only and hold no infra credentials, the upgrade needs the
  controller-side qa-infra checkout and its terraform/ansible credentials, and
  per-phase Jenkins stage status is the acceptance signal; pulling the upgrade into
  the test process would collapse the three stages into one.
- The build tag is `(validation || pit.weekly)`: generic scheduled PIT legs deploy
  Rancher once and never upgrade it, so `SetupSuite` skips the suite when
  `rancherUpgradeInput.targetVersion` is unset. The dedicated pipeline marks its
  config with `rancherUpgradePipeline.enabled: true`; with the marker present but
  the target missing, the suite fails (pipeline bug) instead of skipping.

## Config

```yaml
rancher:
  host: <your_host>
  adminToken: <your_token>
  insecure: true
  clusterName: "local"   # Rancher's own local cluster: the qa-infra-automation RKE2 HA deploy

rancherUpgradeInput:
  targetVersion: "<Rancher version the server was upgraded to, e.g. 2.15.2>"
```
`rancher.clusterName` selects the target cluster. The pipeline uses `local` — Rancher's own
3-node HA RKE2 cluster provisioned by qa-infra-automation — so Longhorn and its volume are
exercised on the server cluster itself, matching what the pipeline deploys.
`rancherUpgradeInput.targetVersion` is required by every phase (the suite skips without it);
the pipeline writes it from the qa-infra-automation resolver output, or set it by hand for
local runs.

## Running

Pre-upgrade (installs Longhorn at the 2.14-era latest and seeds the checksum workload;
run against the not-yet-upgraded Rancher):

```bash
gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/rancherupgrade --junitfile results.xml -- -timeout=60m -tags="validation" -v -run "TestLonghornUpgradeTestSuite/TestLonghornPreUpgrade"
```

Post-upgrade (after `make rancher-upgrade RANCHER_VERSION_TO_UPGRADE=<ver> RANCHER_CHART_REPO_FLAVOR=community|prime`
in qa-infra-automation, and with `rancherUpgradeInput.targetVersion` set):

```bash
gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/rancherupgrade --junitfile results.xml -- -timeout=60m -tags="validation" -v -run "TestLonghornUpgradeTestSuite/TestLonghornPostUpgrade"
```

Chart upgrade:

```bash
gotestsum --format standard-verbose --packages=github.com/rancher/tests/validation/rancherupgrade --junitfile results.xml -- -timeout=60m -tags="validation" -v -run "TestLonghornUpgradeTestSuite/TestLonghornChartUpgrade"
```

## Notes

- The target cluster is Rancher's own local cluster (the qa-infra-automation 3-node HA
  RKE2 deploy), so the server nodes themselves must be sized for Longhorn plus the later
  NeuVector/Observability suites; this is an infra concern and is not configurable here.
- The Rancher server upgrade itself is `make rancher-upgrade` in
  [qa-infra-automation](https://github.com/rancher/qa-infra-automation); this package
  only runs the pre- and post-upgrade assertions around it.
- `make rancher-upgrade` rotates the admin token and writes the new one to
  `ansible/rancher/<env>-ha/generated.tfvars`; the pipeline must copy it into the test
  config's `rancher.adminToken` before the post-upgrade phases (the pre-upgrade token
  is revoked by the upgrade).
- After a server upgrade the cluster-scoped catalog follower can keep serving the
  previous Rancher line's index (e.g. release-v2.14 after upgrading to 2.15) until the
  `cattle-cluster-agent` re-syncs. The chart-upgrade phase detects this and fails with
  an explicit stale-catalog message when the catalog's latest equals the installed
  version, or with "failed to find chart longhorn from the chart repo" on a lookup
  miss; restart the cluster agent and re-run the phase. Observed re-sync time ~4 minutes.
