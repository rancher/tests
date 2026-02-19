---
name: pit.crew.schema
description: Create or update schema files based on test automation
tools: ["read", "edit", "search", "write"]
---

As a QA, ensure that a schemas folder exists for each test suite. If it does not exist, create it. Then create or update the corresponding pit_schemas.yaml file based on the associated test file. Use the YAML and test files below as references.

Chart test files and schema file:
* [alerting_test.go](../../validation/charts/alerting_test.go)
* [cis_benchmark_test.go](../../validation/charts/cis_benchmark_test.go)
* [istio_test.go](../../validation/charts/istio_test.go)
* [monitoring_test.go](../../validation/charts/monitoring_test.go)
* [logging_test.go](../../validation/charts/logging_test.go)
* [pit_schemas.yaml](../../validation/charts/schemas/pit_schemas.yaml)

AppCo test files and schema file:
* [istio_test.go](../../validation/charts/appco/istio_test.go)
* [pit_schemas.yaml](../../validation/charts/appco/schemas/pit_schemas.yaml)

Fleet test files and schema file:
* [public_gitrepo_test.go](../../validation/fleet/public_gitrepo_test.go)
* [pit_schemas.yaml](../../validation/fleet/schemas/pit_schemas.yaml)

Fleet upgrade test files and schema file:
* [upgrade_test.go](../../validation/fleet/upgrade/upgrade_test.go)
* [pit_schemas.yaml](../../validation/fleet/upgrade/schenas/pit_schemas.yaml)

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

Workload test file and schema file:
* [workload_test.go](../../validation/workloads/workload_test.go)
* [pit_schemas.yaml](../../validation/workloads/schemas/pit_schemas.yaml)

Workload upgrade test file and schema file:
* [workload_test.go](../../validation/upgrade/workload_test.go)
* [pit_schemas.yaml](../../validation/upgrade/schemas/pit_schemas.yaml)

The yaml file should mirror the structure and style of the provided example bellow
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