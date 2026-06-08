---
name: pit.crew.schema
description: Create or update PIT schema YAML files
tools: ["view", "edit", "grep", "glob", "create"]
---

As a QA, ensure that a schemas folder exists for each test suite. If it does not exist, create it. Then create or update the corresponding pit_schemas.yaml file based on the associated test file. Use the YAML and test files below as references.

## Rules

- The value of `custom_field["15"]` must exactly match the Go test function name (e.g., `"TestCISBenchmarkInstallation"`). Read the test file to find all `Test*` functions and create one case per function.
- Each high-level action in the test function body maps to one step entry. Steps must be sequential and numbered starting at 1.
- Always use `[RANCHERINT]` (no spaces) as the projects value.
- Use `automation: 2` for all cases.

## Test files and schema files

Chart test files and schema file:
* [alerting_test.go](../../validation/charts/alerting_test.go)
* [cis_benchmark_test.go](../../validation/charts/cis_benchmark_test.go)
* [istio_test.go](../../validation/charts/istio_test.go)
* [monitoring_test.go](../../validation/charts/monitoring_test.go)
* [logging_test.go](../../validation/charts/logging_test.go)
* [neuvector_test.go](../../validation/charts/neuvector_test.go)
* [pit_schemas.yaml](../../validation/charts/schemas/pit_schemas.yaml)

AppCo test files and schema file:
* [istio_test.go](../../validation/charts/appco/istio_test.go)
* [pit_schemas.yaml](../../validation/charts/appco/schemas/pit_schemas.yaml)

Fleet test files and schema file:
* [public_gitrepo_test.go](../../validation/fleet/public_gitrepo_test.go)
* [pit_schemas.yaml](../../validation/fleet/schemas/pit_schemas.yaml)

Fleet airgap test files and schema file:
* [fleet_airgap_test.go](../../validation/fleet/airgap/fleet_airgap_test.go)
* [pit_schemas.yaml](../../validation/fleet/airgap/schemas/pit_schemas.yaml)

Fleet upgrade test files and schema file:
* [upgrade_test.go](../../validation/fleet/upgrade/upgrade_test.go)
* [pit_schemas.yaml](../../validation/fleet/upgrade/schemas/pit_schemas.yaml)

Longhorn chart installation test files and schema file:
* [installation_test.go](../../validation/longhorn/chartinstall/installation_test.go)
* [pit_schemas.yaml](../../validation/longhorn/chartinstall/schemas/pit_schemas.yaml)

Longhorn chart test files and schema file:
* [longhorn_test.go](../../validation/longhorn/longhorn_test.go)
* [pit_schemas.yaml](../../validation/longhorn/schemas/pit_schemas.yaml)

Connectivity test files and schema file:
* [network_policy_test.go](../../validation/networking/connectivity/network_policy_test.go)
* [port_test.go](../../validation/networking/connectivity/port_test.go)
* [pit_schemas.yaml](../../validation/networking/connectivity/schemas/pit_schemas.yaml)

NeuVector test files and schema file:
* [neuvector_hardened_test.go](../../validation/neuvector/neuvector_hardened_test.go)
* [pit_schemas.yaml](../../validation/neuvector/schemas/pit_schemas.yaml)

Certificates RKE2 test files and schema file:
* [cert_rotation_test.go](../../validation/certificates/rke2/cert_rotation_test.go)
* [cert_rotation_wins_test.go](../../validation/certificates/rke2/cert_rotation_wins_test.go)
* [pit_schemas.yaml](../../validation/certificates/rke2/schemas/pit_schemas.yaml)

Snapshot RKE2 test files and schema file:
* [snapshot_restore_test.go](../../validation/snapshot/rke2/snapshot_restore_test.go)
* [snapshot_recurring_test.go](../../validation/snapshot/rke2/snapshot_recurring_test.go)
* [snapshot_s3_restore_test.go](../../validation/snapshot/rke2/snapshot_s3_restore_test.go)
* [snapshot_retention_test.go](../../validation/snapshot/rke2/snapshot_retention_test.go)
* [snapshot_restore_wins_test.go](../../validation/snapshot/rke2/snapshot_restore_wins_test.go)
* [pit_schemas.yaml](../../validation/snapshot/rke2/schemas/pit_schemas.yaml)

Node scaling RKE2 test files and schema file:
* [scaling_test.go](../../validation/nodescaling/rke2/scaling_test.go)
* [pit_schemas.yaml](../../validation/nodescaling/rke2/schemas/pit_schemas.yaml)

Provisioning RKE2 test files and schema file:
* [node_driver_test.go](../../validation/provisioning/rke2/node_driver_test.go)
* [custom_test.go](../../validation/provisioning/rke2/custom_test.go)
* [pit_schemas.yaml](../../validation/provisioning/rke2/schemas/pit_schemas.yaml)

Workload test file and schema file:
* [workload_test.go](../../validation/workloads/workload_test.go)
* [pit_schemas.yaml](../../validation/workloads/schemas/pit_schemas.yaml)

Workload upgrade test file and schema file:
* [workload_test.go](../../validation/upgrade/workload_test.go)
* [pit_schemas.yaml](../../validation/upgrade/schemas/pit_schemas.yaml)

## Example YAML

The yaml file should mirror the structure and style of the provided example below:
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