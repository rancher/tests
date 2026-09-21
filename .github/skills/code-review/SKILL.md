---
name: code-review
description: >-
    Review staged, unstaged, or branch diffs in the rancher/tests repository against this
    repo's conventions for Go test files, Jenkinsfiles, and actions/extensions organization.
    Use when asked to review a PR, diff, or set of changed files in this repository, or before
    opening a PR.
user-invocable: true
---

# rancher/tests Code Review

Repository-specific checklist for reviewing changes in `rancher/tests`. This skill
supplements the general-purpose code-review agent with rules unique to this codebase.
Always find a concrete diff to review first (`git diff`, `git diff --staged`, or a PR's
changed files) — do not review from memory without seeing the actual changes.

## When to use it

- Reviewing a pull request or local diff in this repository before it is submitted or merged
- Auditing a batch of changed `*_test.go` files or Jenkinsfiles for repo-standard compliance
- Double-checking a new action/extension/helper function was placed in the right location

## Instructions

1. Identify the diff to review:
   ```bash
   git --no-pager diff HEAD           # unstaged
   git --no-pager diff --staged       # staged
   git --no-pager diff main...HEAD    # branch vs main
   ```
2. For every changed `*_test.go` file, walk the **Go test review** checklist below.
3. For every changed `Jenkinsfile*` (under `validation/`, `validation/pipeline/`, or
   subdirectories like `rancherha/`, `qainfra/`), walk the **Jenkinsfile review**
   checklist below.
4. For every new/changed helper function, check it against the **Actions Vs. Extensions**
   criteria in the repo's root `copilot-instructions.md` (single source of truth for that
   distinction).
5. Report only high-confidence issues tied to a specific file/line. Do not comment on
   style or formatting already enforced by `golangci-lint`.
6. Summarize findings grouped by severity, referencing file paths and line numbers.

## Go test review checklist

When judging quality/style of a changed test file, compare it against these reference
files, which represent the expected standard — treat deviations from their patterns as
signal, not just this checklist:
- Charts: `validation/charts/alerting_test.go`, `cis_benchmark_test.go`, `istio_test.go`,
  `monitoring_test.go`, `logging_test.go`, `neuvector_test.go`,
  `validation/charts/appco/istio_test.go`
- Fleet: `validation/fleet/public_gitrepo_test.go`, `validation/fleet/upgrade/upgrade_test.go`
- Longhorn: `validation/longhorn/chartinstall/installation_test.go`,
  `validation/longhorn/longhorn_test.go`
- Networking: `validation/networking/connectivity/network_policy_test.go`, `port_test.go`
- Workloads: `validation/workloads/workload_test.go`, `validation/upgrade/workload_test.go`

**Build tags**
- Every test file has a `//go:build` line with appropriate feature/cluster/tier tags
- Version tags follow existing patterns (e.g. `2.13`, `!2.8`) — see `TAG_GUIDE.md`
- PIT tags identify the scheduled test tier; verify their schedule and airgap compatibility against `TAG_GUIDE.md` rather than requiring external API calls.

**File and package structure**
- Helper functions specific to a test package live in a separate file in the same
  package directory that does **not** end in `_test.go` (e.g. `validation/charts/monitoring.go`
  alongside `validation/charts/monitoring_test.go`)
- Helper functions generalizable across packages belong in `actions/`
- No unnecessary or multiple layers of abstraction; no duplicated code

**Naming conventions**
- Public functions use `UpperCamelCase`; private functions use `lowerCamelCase`
- The receiver variable uses the first letter of the test suite struct name (e.g.
  `func (m *MonitoringTestSuite)`, not `func (i *MonitoringTestSuite)`)
- Test names are not redundant with the suite name (prefer
  `LoggingTestSuite.TestChartInstallation` over `LoggingTestSuite.TestLoggingInstallation`)
- Test and suite names are not ambiguous
- Feature name appears only in the suite name (`SuiteName`), never in individual test names

**Test logic**
- Waits and validations live in the test logic itself, not in helper or library functions
- Validation always happens in a separate function, not inline in the test method
- Arrays/slices are validated as non-empty when applicable (a bare `for` loop can silently
  hide an empty-collection bug)
- CRDs are installed before other charts that depend on them are deployed
- CRDs are uninstalled only after the charts depending on them are removed
- The suite-level session is created in `SetupSuite` and its `session.Cleanup()` runs in
  `TearDownSuite` only
- Individual tests that need per-test scoped resources create a sub-session
  (`subSession := s.session.NewSession()`) and clean it up locally with
  `defer subSession.Cleanup()` inside the test — this is the expected pattern, not a
  violation of suite-level cleanup rules
- At least 2 tests per suite when possible: one containing `Dynamic` (config-driven, via
  `github.com/rancher/shepherd/pkg/config`) and one fully static (no config input)
- Test/helper files follow naming: `*_test.go` for tests, `<feature>.go` for helpers,
  `deprecated_*_test.go` for deprecated tests

**Helper functions**
- Default to returning an `error` instead of accepting a `*testing.T` parameter. Exception:
  a `*testing.T` param is acceptable when the helper must emit structured test logs/assertions
  as part of its own responsibility (e.g. a reusable validation helper that logs context on failure)
- Return pointers to objects where possible
- Function parameters use primitive types whenever possible
- Avoid adding new parameters to `Create` calls unless strictly necessary
- GoDoc comments are required for all exported functions
- Ignored errors are justified with an inline comment explaining why

**Logging and output**
- In suite methods (`SetupSuite`, `TearDownSuite`, `SetupTest`, `TearDownTest`, all `Test*`
  methods), use `s.T().Log()` / `s.T().Logf()` — never `logrus` or `fmt.Print*` there
- Logs clearly describe test steps to improve traceability
- No `time.Sleep` calls anywhere — use appropriate polls/watches instead

**Constants and strings**
- Strings are a `const` wherever possible, except in log messages and error strings

**API usage**
- Prefer SteveV1 or public APIs over command-line interactions

**Code organization**
- Shared logic used by multiple test packages lives in `actions/`, not duplicated inline
- Unless the test is `pit`-tagged, only `actions/` or shepherd extension helpers are used —
  no direct calls to non-Rancher/non-shepherd external APIs

## Jenkinsfile review checklist

Compare changed Jenkinsfiles against these reviewed and merged reference pipelines —
treat deviations from their patterns as signal, not just this checklist:
- Modern declarative (preferred pattern): `validation/Jenkinsfile.e2e`,
  `validation/Jenkinsfile.individual.e2e`
- Legacy scripted (compatibility reference only): `validation/Jenkinsfile`,
  `validation/Jenkinsfile.harvester`
- Airgap: `validation/pipeline/Jenkinsfile.airgap-rke2-tests`,
  `validation/pipeline/Jenkinsfile.setup.airgap-rke2-infra`,
  `validation/pipeline/Jenkinsfile.destroy.airgap-rke2-infra`
- Recurring: `validation/pipeline/Jenkinsfile.recurring`,
  `validation/pipeline/Jenkinsfile.multibranch.recurring`
- Rancher HA: `validation/pipeline/rancherha/Jenkinsfile.ha.deploy`
- QA infra / Elemental: `validation/pipeline/qainfra/Jenkinsfile.elemental.e2e`,
  `validation/pipeline/qainfra/Jenkinsfile.elemental.harvester.e2e`

**Shared library usage**
- All pipelines load the `qa-jenkins-library` shared library using:
  ```groovy
  def libraryBranch = env.QA_JENKINS_LIBRARY_BRANCH ?: 'main'
  library "qa-jenkins-library@${libraryBranch}"
  ```
- Infrastructure operations use library abstractions (`make.runTarget`, `tofu.*`,
  `infrastructure.*`, `airgap.standardCheckout`) — never raw `sh` commands for these
  directly in a Jenkinsfile
- Test execution uses library helpers (`property.useWithProperties`) rather than ad-hoc
  credential or environment wiring
- Common logic shared across multiple Jenkinsfiles lives in the shared library, not
  duplicated inline

**Pipeline structure**
- New pipelines use declarative `pipeline {}` syntax; scripted `node {}` pipelines are
  legacy and should not be created
- Pipeline logic in the Jenkinsfile is minimal — orchestration only; business logic
  belongs in shared library functions
- No unnecessary layers of abstraction within the Jenkinsfile itself

**Parameters and configuration**
- All configurable values (paths, images, timeouts, tags, credentials, regions, repo
  URLs) are pipeline parameters or sourced from library helpers — never hardcoded
- Environment variable reads use `?:` (Elvis operator) for safe defaults (e.g.
  `env.TIMEOUT ?: '60m'`)
- Input validation is present for all public functions: required map keys are checked
  and meaningful errors raised when missing
- `text` parameters use `defaultValue: '''...'''` (Groovy triple-quoted), never
  `default: |` (YAML pipe syntax)
- `library` directive at the top uses `env.X`, never `params.X` (params aren't resolved
  at library load time)
- Paths use `env.*_DIR` variables set by `airgap.standardCheckout`, not hardcoded
  directory names like `'qa-infra-automation'`
- Ansible variable templates reference pipeline parameters (e.g.
  `${RANCHER_BOOTSTRAP_PASSWORD}`), never hardcoded values like `"rancherrocks"`
- Boolean-like Ansible variables (`enable_private_registry`, `deploy_rancher`) reflect
  pipeline parameters instead of being hardcoded to `true`

**Error handling and cleanup**
- Error handling is explicit: `error(message)` with clear messages
- Cleanup logic (containers, workspaces, artifacts, infrastructure teardown) executes on
  failure as well as success — via `post { always { } }` or `try/catch/finally`
- Infrastructure teardown guards against double-cleanup using a flag (e.g.
  `env.INFRA_CLEANED == 'true'`)
- Teardown failures are caught and logged without masking the original build failure

**Credentials and secrets**
- No hardcoded secrets in `defaultValue` blocks — use `${VARIABLE}` substitution and
  `password` parameter types
- All credentials (registry passwords, Rancher passwords, API keys) use `password` type
  parameters, handled exclusively through Jenkins credentials bindings or library helpers
- Secrets are never logged, echoed, or hardcoded in any form
- `useWithProperties` blocks include every credential referenced in their scope's
  substitution maps

**Workspace hygiene**
- Workspace usage is deterministic and isolated: clean checkout, predictable directory
  names, no cross-build contamination
- Container and volume names include `${JOB_NAME}` and `${BUILD_NUMBER}` for uniqueness

**Naming conventions**
- Centralized naming utilities from the shared library are used rather than ad-hoc
  string concatenation for job names, container names, and workspace names
- Variable names use `lowerCamelCase` in Groovy

**Artifacts and results**
- Test results/artifacts are always archived, even in partial-failure scenarios where
  results are available, via `junit` and `archiveArtifacts` steps
- `archiveArtifacts` avoids `fingerprint: true` for transient build artifacts

**Readability**
- Jenkinsfiles include a top-level Groovy doc comment (`/** ... */`) describing the
  pipeline's purpose, what it does, and which shared library functions it consumes —
  following the pattern in `Jenkinsfile.e2e`/`Jenkinsfile.individual.e2e`
- Inline comments explain non-obvious decisions (e.g. why a `TODO` exists)

**Pinned versions**
- Docker image and library references use pinned digests or explicit version tags —
  never `latest`

## When not to use it

- Reviewing changes outside `rancher/tests` conventions (e.g. unrelated repos) — use the
  general-purpose `code-review` agent instead
- No diff or changed files are available to review
