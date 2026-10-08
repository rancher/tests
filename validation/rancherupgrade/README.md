# Rancher Upgrade

The `rancherupgrade` package validates upgrading the Rancher server (2.14.x -> latest)
with interoperability products installed. The first vertical is Longhorn: a pre-upgrade
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

## Config

```yaml
rancher:
  host: <your_host>
  adminToken: <your_token>
  insecure: true
  clusterName: "<downstream RKE2 cluster name>"   # pre-provisioned by qa-infra-automation

rancherUpgradeInput:
  targetVersion: "<Rancher version the server was upgraded to, e.g. 2.15.2>"
```

`rancher.clusterName` selects the shared downstream cluster. `rancherUpgradeInput.targetVersion`
is required by the post-upgrade and chart-upgrade phases; the pipeline writes it from
the qa-infra-automation resolver output, or set it by hand for local runs.

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

- The shared downstream RKE2 cluster is provisioned by qa-infra-automation and must be
  sized for Longhorn plus the later NeuVector/Observability suites; this is an infra
  concern and is not configurable here.
- The Rancher server upgrade itself is `make rancher-upgrade` in
  [qa-infra-automation](https://github.com/rancher/qa-infra-automation); this package
  only runs the pre- and post-upgrade assertions around it.
- `make rancher-upgrade` rotates the admin token and writes the new one to
  `ansible/rancher/<env>-ha/generated.tfvars`; the pipeline must copy it into the test
  config's `rancher.adminToken` before the post-upgrade phases (the pre-upgrade token
  is revoked by the upgrade).
- After a server upgrade the downstream cluster-scoped catalog follower can keep
  serving the previous Rancher line's index (e.g. release-v2.14 after upgrading to
  2.15) until the downstream `cattle-cluster-agent` re-syncs. If the chart-upgrade
  phase fails with "failed to find chart longhorn from the chart repo", restart the
  downstream cluster-agent and retry; observed re-sync time ~4 minutes.
