# Jenkins Pipelines

This directory holds the Jenkins pipelines for Rancher QA infrastructure and
test execution. All shared logic lives in the [`qa-jenkins-library`](https://github.com/rancher/qa-jenkins-library)
shared library — Jenkinsfiles here contain only parameters, stage wiring, and
pipeline-specific conditionals.

## Shared library architecture

All shared functions live in `qa-jenkins-library` `vars/*.groovy`, configured
in Jenkins as a Global Pipeline Library. There is intentionally **no local
`vars/` directory** in rancher/tests — the single-tier decision from PRD #585
means every shared function belongs in the library, never in the repo.

Every Jenkinsfile loads the library with exactly this header:

```groovy
def libraryBranch = env.QA_JENKINS_LIBRARY_BRANCH ?: 'main'
library "qa-jenkins-library@${libraryBranch}"
```

Use `env`, never `params`, in this header — parameters are not resolved at
library-load time.

### Library modules

| Module | Functions |
|---|---|
| `airgap.groovy` | `standardCheckout`, `configureAnsible`, `teardownInfrastructure`, `deployRKE2`, `deployRancher` |
| `make.groovy` | `runTarget` |
| `infrastructure.groovy` | `parseAndSubstituteVars`, `writeConfig`, `generateWorkspaceName`, `archiveWorkspaceName`, `writeSshKey`, `cleanupArtifacts` |
| `tofu.groovy` | `initBackend`, `createWorkspace`, `selectWorkspace`, `apply`, `destroy`, `deleteWorkspace`, `getOutputs`, `teardownInfrastructure` |
| `ansible.groovy` | `runPlaybook` |
| `property.groovy` | `useWithProperties` |
| Supporting | `config.groovy`, `container.groovy`, `generate.groovy`, `naming.groovy`, `project.groovy`, `result.groovy` |

Every function is documented with a `Parameters:` docblock in its source file
(see `vars/airgap.groovy` in the library repo for the style). Read those
docblocks before calling a function — they are the authoritative signatures.

## Naming convention

Infra lifecycle pairs use `Jenkinsfile.<action>.<category>-<variant>`:

- `Jenkinsfile.setup.airgap-rke2-infra`
- `Jenkinsfile.destroy.airgap-rke2-infra`

Setup and destroy are separate files rather than one pipeline with an ACTION
parameter because they need:

- different timeouts (180 vs 120 minutes),
- different parameter sets (destroy needs `TARGET_WORKSPACE`, not the full
  deploy matrix),
- separate Jenkins job access control.

Single-purpose pipelines use descriptive names: `Jenkinsfile.airgap-rke2-tests`,
`../Jenkinsfile.validation`.

## Creating a new pipeline

1. Copy the closest reference: [Jenkinsfile.setup.airgap-rke2-infra](Jenkinsfile.setup.airgap-rke2-infra)
   for infra pipelines, [Jenkinsfile.validation](../Jenkinsfile.validation) for
   simple Dockerized test runners.
2. Keep the `env.QA_JENKINS_LIBRARY_BRANCH` library header verbatim.
3. Declare parameters in the `parameters {}` block using the conventions in
   [Parameter conventions](#parameter-conventions).
4. Check out with:

   ```groovy
   def dirs = airgap.standardCheckout(
       testsRepo: [url: params.TESTS_REPO_URL, branch: params.TESTS_BRANCH],
       infraRepo: [url: params.QA_INFRA_REPO_URL, branch: params.QA_INFRA_BRANCH]
   )
   env.TESTS_DIR = dirs.testsDir
   env.INFRA_DIR = dirs.infraDir
   ```

   Use `env.TESTS_DIR` / `env.INFRA_DIR` for all paths — never hardcode
   directory names.
5. Bind credentials with `property.useWithProperties([...]) { ... }` and put
   all infra stages inside that closure.
6. Implement stages by calling shared functions / `make.runTarget` — never raw
   `sh` for tofu/ansible/make operations. Write tfvars via
   `infrastructure.parseAndSubstituteVars` + `infrastructure.writeConfig`.
   Persist the workspace name via `infrastructure.archiveWorkspaceName`.
7. Add a `post { failure { ... } }` teardown gated on `DESTROY_ON_FAILURE`
   using `airgap.teardownInfrastructure`. Test pipelines additionally gate a
   `post { always }` teardown on `DESTROY_AFTER_TESTS`.
8. Add a JJB definition in [rancherlabs/jenkins-job-builder](https://github.com/rancherlabs/jenkins-job-builder)
   following `qa-airgap-rke2-infra.yml`: one `defaults` + one `job` per
   pipeline, one `view` grouping them, `pipeline-scm` with
   `url: https://github.com/rancher/tests`, `branches: [main]`, the
   `script-path`, `lightweight-checkout: true`, `folder: rancher_qa`. JJB
   `text` parameters use `default: |` YAML blocks whose content contains
   `${VAR}` placeholders for the pipeline to substitute.
9. Live-verify the new job at least twice before archiving any predecessor —
   parallel coexistence rule (PRD #585, user story 25).

## Makefile integration pattern

The [qa-infra-automation](https://github.com/rancher/qa-infra-automation)
Makefile is the control point for cluster-scoped operations. It is invoked via
`make.runTarget(target:, dir:, makeArgs:, ...)`, which runs the target in the
`rancher-infra-tools` Docker container with workspace/SSH mounts and env
forwarding.

Targets used by the pipelines:

| Target | Purpose |
|---|---|
| `backend-s3` | Initialize the tofu S3 backend |
| `workspace-new` | Create a tofu workspace |
| `infra-up` | Apply infrastructure + generate the airgap inventory |
| `cluster` | Deploy RKE2 via Ansible |
| `registry` | Configure the private registry (conditional on `PRIVATE_REGISTRY_URL`) |
| `rancher` | Deploy Rancher via Helm |

**Exception — destroy does NOT use the Makefile.** From the header of
[Jenkinsfile.destroy.airgap-rke2-infra](Jenkinsfile.destroy.airgap-rke2-infra):

> Teardown uses the Jenkins library directly (NOT make infra-down) because:
> - The Makefile's infra-down target has an interactive confirmation prompt
> - The Makefile has no workspace management (select/delete)
> - The Jenkins library's tofu.teardownInfrastructure handles the full
>   sequence: selectWorkspace → destroy → deleteWorkspace

Destroy calls `airgap.teardownInfrastructure(dir:, name:, varFile:)` directly.

## Parameter conventions

Harmonized across all new pipelines:

- **Standard repo params:** `QA_JENKINS_LIBRARY_BRANCH`, `TESTS_REPO_URL`,
  `TESTS_BRANCH`, `QA_INFRA_REPO_URL`, `QA_INFRA_BRANCH` — defaults are the
  upstream repos and `main`.
- **Destroy gates:** `DESTROY_ON_FAILURE` (boolean, default `true`) everywhere;
  test pipelines add `DESTROY_AFTER_TESTS` (boolean, default `true`). Same
  name = same semantics across pipelines.
- **Secrets are always `password` type, never `string`:**
  `PRIVATE_REGISTRY_PASSWORD`, `RANCHER_BOOTSTRAP_PASSWORD`,
  `RANCHER_ADMIN_PASSWORD`. No hardcoded secret defaults.
- **Bulk config as `text` params:** `TERRAFORM_CONFIG`, `ANSIBLE_VARIABLES`,
  `CATTLE_TEST_CONFIG`. Defaults are supplied by the JJB job template, not the
  Jenkinsfile; `${VAR}` placeholders inside are substituted by
  `infrastructure.parseAndSubstituteVars`.
- **Parameterized test-runner agent** via a `choice` param:

  ```groovy
  agent { label params.NODE_LABEL ?: '' }
  ```

  with choices `['', 'harvester-vpn-1', 'vsphere-vpn-1']` (pattern from
  [Jenkinsfile.validation](../Jenkinsfile.validation)).

## Contributing to qa-jenkins-library

A new shared function is a PR to [rancher/qa-jenkins-library](https://github.com/rancher/qa-jenkins-library):

1. Branch off `main`.
2. Add the function in the appropriate `vars/*.groovy` with a `Parameters:`
   docblock (follow `vars/airgap.groovy` style).
3. Add JenkinsPipelineUnit tests and ensure `./gradlew test` passes — PR #18
   added 134 tests; new functions must not reduce coverage.
4. Open the PR.
5. Before merge, test the change from a real pipeline by setting the job's
   `QA_JENKINS_LIBRARY_BRANCH` parameter to the feature branch (or
   `library "qa-jenkins-library@pull/<PR>/head"`).

## Migration status (old → new)

| Old file | Disposition | Replacement |
|---|---|---|
| `validation/pipeline/tfp/Jenkinsfile.airgap.tests` | Deleted (Slice 5) | `validation/pipeline/Jenkinsfile.airgap-rke2-tests` |
| `validation/pipeline/Jenkinsfile.setup.airgap.rke2` | Moved to `validation/deprecated/` (Slice 5) | `validation/pipeline/Jenkinsfile.setup.airgap-rke2-infra` |
| `validation/pipeline/Jenkinsfile.destroy.airgap.rke2` | Moved to `validation/deprecated/` (Slice 5) | `validation/pipeline/Jenkinsfile.destroy.airgap-rke2-infra` |
| `validation/pipeline/Jenkinsfile.airgap.go-tests` | Deleted (PR #598) | `validation/pipeline/Jenkinsfile.airgap-rke2-tests` |
| `validation/Jenkinsfile.upgrade.e2e` | Moved to `validation/deprecated/` (Slice 5) | GHA workflow `day2-ops-upgraded-rancher` (Go-based upgrades) |
| `validation/Jenkinsfile` | Retained — live `script-path` for other teams' JJB jobs (`qa-go-automation-test.yml`, `qa-go-provisioning-sanity-check.yml`, `distros/rancher-automation.yaml`) | `validation/Jenkinsfile.validation` (`NODE_LABEL=''`); adopt via owning team's own JJB PR |
| `validation/Jenkinsfile.harvester` | Retained — live consumer `qa-go-automation-test.yml` | `validation/Jenkinsfile.validation` (`NODE_LABEL=harvester-vpn-1`) |
| `validation/Jenkinsfile.vsphere` | Retained — live consumer `qa-go-automation-test.yml` | `validation/Jenkinsfile.validation` (`NODE_LABEL=vsphere-vpn-1`) |
| `validation/Jenkinsfile.e2e` | Active — excluded from consolidation in #592 (subdirectory checkout, different Dockerfile, Docker volumes); used by pit-crew `qa-pit-runs.yml` / `qa-recurring-runs.yml` | — (remains itself) |
