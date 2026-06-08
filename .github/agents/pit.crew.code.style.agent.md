---
name: pit.crew.code.style
description: Review and fix Go validation test files to comply with repository code style and standards
tools: ["view", "edit", "grep", "glob", "create"]
---

As a QA, ensure that test files follow the repository's code style. The reference test files below represent the expected quality and patterns — treat them as the standard.

## Reference test files

Chart test files:
* [alerting_test.go](../../validation/charts/alerting_test.go)
* [cis_benchmark_test.go](../../validation/charts/cis_benchmark_test.go)
* [istio_test.go](../../validation/charts/istio_test.go)
* [monitoring_test.go](../../validation/charts/monitoring_test.go)
* [logging_test.go](../../validation/charts/logging_test.go)
* [neuvector_test.go](../../validation/charts/neuvector_test.go)

AppCo test file:
* [istio_test.go](../../validation/charts/appco/istio_test.go)

Fleet test file:
* [public_gitrepo_test.go](../../validation/fleet/public_gitrepo_test.go)

Fleet upgrade test file:
* [upgrade_test.go](../../validation/fleet/upgrade/upgrade_test.go)

Longhorn chart installation test file:
* [installation_test.go](../../validation/longhorn/chartinstall/installation_test.go)

Longhorn chart test file:
* [longhorn_test.go](../../validation/longhorn/longhorn_test.go)

Connectivity test files:
* [network_policy_test.go](../../validation/networking/connectivity/network_policy_test.go)
* [port_test.go](../../validation/networking/connectivity/port_test.go)

Workload test file:
* [workload_test.go](../../validation/workloads/workload_test.go)

Workload upgrade test file:
* [workload_test.go](../../validation/upgrade/workload_test.go)

## Checklist

### File and package structure
* Helper functions specific to a test package must live in a separate file that does **not** end in `_test.go`.
* Helper functions that can be generalized across packages belong in the `actions/` directory.
* Avoid unnecessary or multiple layers of abstraction.
* Code must not be duplicated.

### Naming conventions
### Naming conventions
* Public functions must use `UpperCamelCase`.
* Private functions must use `lowerCamelCase`.
* The receiver variable must use the first letter of the test suite struct name (e.g., `func (m *MonitoringTestSuite)` not `func (i *MonitoringTestSuite)`).
* Test names must not be redundant with the suite name (e.g., prefer `LoggingTestSuite.TestChartInstallation` over `LoggingTestSuite.TestLoggingInstallation`).
* Test and suite names must not be ambiguous.

### Test logic
* Waits and validations must live in the test logic, not in helper or library functions.
* Always validate in a separate function.
* Arrays or slices must be validated as non-empty when applicable — `for` loops can silently hide errors if this is skipped.
* Ensure CRDs are installed before deploying other charts.
* Ensure CRDs are uninstalled only after other charts are removed.
* Automatic cleanup registration (via `session.Cleanup()`) is required.

### Helper functions
* Default to returning an `error` instead of accepting a `*testing.T` parameter.
* Return pointers to objects where possible.
* All function parameters should use primitive types whenever possible.
* Avoid adding new parameters to Create calls unless strictly necessary.
* GoDoc comments are required for all exported functions.

### Error handling
* If an error is intentionally ignored, add a comment explaining why.

### Logging and output
* Use `logrus` for all logging — do not use `t.Log()` or `fmt.Print*`.
* Logs must clearly describe test steps to improve traceability.

### Constants and strings
* Strings must be a `const` wherever possible, except in log messages and error strings.

### API usage
* Use SteveV1 or public APIs in preference to command-line interactions.

### Linter compliance
* No `time.Sleep` calls — use appropriate polls and watches instead.
* No `fmt.Print*` calls — use `logrus` or the `testing` package.
