---
name: schema
description: >-
    Create or update PIT schema pit_schemas.yaml files for a test package in the
    rancher/tests repository, based on the package's Go test file(s). Use after writing or
    modifying a `*_test.go` file in a PIT-tagged package (e.g. `pit.daily`, `pit.weekly`),
    or when asked to create/sync a schemas folder or pit_schemas.yaml file.
user-invocable: true
---

# rancher/tests PIT Schema Generator

As a QA, ensure that a `schemas` folder exists for each test suite/package. If it does
not exist, create it. Then create or update the corresponding `pit_schemas.yaml` file
in that folder based on the associated test file(s).

## When to use it

- After writing or modifying `*_test.go` files in a PIT-tagged package
- A package's `schemas/pit_schemas.yaml` is missing, stale, or out of sync with its
  `Test*` functions
- Asked to generate a schema file for a new or updated test suite

## Instructions

1. Find the package directory of the changed/new `*_test.go` file(s).
2. Check whether `<package>/schemas/pit_schemas.yaml` exists. If not, create the `schemas` folder and the file.
3. Read the PIT-tagged test file(s) for the package to enumerate each suite test method whose name starts with `Test`; exclude the package-level `Test...TestSuite` wrapper that only calls `suite.Run`.
4. For each suite test method, create or update one `cases` entry, following the **Rules** and **Example YAML** below.
5. Read through each test function body and map every high-level action (e.g. cluster
   setup, chart install, scan/verification step) to one sequential `steps` entry,
   numbered starting at 1 via `position`.
6. Keep existing, still-accurate entries untouched; only add/update entries for
   functions that are new or have changed.

## Rules

- The value of `custom_field["15"]` must exactly match the Go test function name (e.g.
  `"TestCISBenchmarkInstallation"`). Read the test file to find all `Test*` functions
  and create one case per function.
- Each high-level action in the test function body maps to one step entry. Steps must
  be sequential and numbered starting at 1.
- Always use `[RANCHERINT]` (no spaces) as the `projects` value.
- Use `automation: 2` for all cases.

## Reference test files and schema files

Use these existing pairs as the pattern to follow for structure and level of step
detail:

- Chart: `validation/charts/{alerting,cis_benchmark,istio,monitoring,logging,neuvector}_test.go`
  → `validation/charts/schemas/pit_schemas.yaml`
- AppCo: `validation/charts/appco/istio_test.go`
  → `validation/charts/appco/schemas/pit_schemas.yaml`
- Fleet: `validation/fleet/public_gitrepo_test.go`
  → `validation/fleet/schemas/pit_schemas.yaml`
- Fleet airgap: `validation/fleet/airgap/fleet_airgap_test.go`
  → `validation/fleet/airgap/schemas/pit_schemas.yaml`
- Fleet upgrade: `validation/fleet/upgrade/upgrade_test.go`
  → `validation/fleet/upgrade/schemas/pit_schemas.yaml`
- Longhorn chart installation: `validation/longhorn/chartinstall/installation_test.go`
  → `validation/longhorn/chartinstall/schemas/pit_schemas.yaml`
- Longhorn chart: `validation/longhorn/longhorn_test.go`
  → `validation/longhorn/schemas/pit_schemas.yaml`
- Connectivity: `validation/networking/connectivity/{network_policy,port}_test.go`
  → `validation/networking/connectivity/schemas/pit_schemas.yaml`
- NeuVector: `validation/neuvector/neuvector_hardened_test.go`
  → `validation/neuvector/schemas/pit_schemas.yaml`
- Certificates RKE2: `validation/certificates/rke2/{cert_rotation,cert_rotation_wins}_test.go`
  → `validation/certificates/rke2/schemas/pit_schemas.yaml`
- Snapshot RKE2: `validation/snapshot/rke2/{snapshot_restore_etcd,snapshot_restore_k8s_upgrade,snapshot_restore_upgrade_strategy,snapshot_recurring,snapshot_s3_restore,snapshot_retention,snapshot_restore_wins}_test.go`
  → `validation/snapshot/rke2/schemas/pit_schemas.yaml`
- Node scaling RKE2: `validation/nodescaling/rke2/scaling_test.go`
  → `validation/nodescaling/rke2/schemas/pit_schemas.yaml`
- Provisioning RKE2: `validation/provisioning/rke2/{node_driver,custom}_test.go`
  → `validation/provisioning/rke2/schemas/pit_schemas.yaml`
- Workload: `validation/workloads/workload_test.go`
  → `validation/workloads/schemas/pit_schemas.yaml`
- Workload upgrade: `validation/upgrade/workload_test.go`
  → `validation/upgrade/schemas/pit_schemas.yaml`

## Example YAML

The yaml file should mirror the structure and style of the example below:

```yaml
- projects: [RANCHERINT]
  suite: Charts
  cases:
  - title: "CIS Benchmark Chart Installation and Scan"
    description: "Verify CIS Benchmark chart installation and successful execution of CIS scan"
    automation: 2
    steps:
    - action: "Config a downstream cluster running in rancher"
      data: ""
      expectedresult: "A downstream cluster is active and ready to receive workloads"
      position: 1
    - action: "Create Project"
      data: "Project name: System"
      expectedresult: "The 'System' project is successfully created"
      position: 2
    - action: "Install CIS Benchmark chart"
      data: "helm install rancher-cis-benchmark ./ --create-namespace -n cis-operator-system"
      expectedresult: "CIS Benchmark chart installs successfully"
      position: 3
    - action: "Run CIS benchmark scan"
      data: "Profile name: cis-1.11-profile"
      expectedresult: "CIS scan completes successfully"
      position: 4
    custom_field:
      "15": "TestCISBenchmarkInstallation"
```

## When not to use it

- The test file being added/changed is not `pit`-tagged and has no corresponding
  `schemas/pit_schemas.yaml` requirement
- Generating schemas for a repository other than `rancher/tests`
