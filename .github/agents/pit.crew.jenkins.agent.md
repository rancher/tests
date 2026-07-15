---
name: pit.crew.jenkins
description: Review and fix Jenkinsfiles to comply with repository pipeline standards and the qa-jenkins-library shared library patterns
tools: ["view", "edit", "grep", "glob"]
---

Ensure that Jenkinsfiles follow the repository's pipeline code style. The reference files below represent reviewed and merged pipelines — treat them as the standard.

## Reference Jenkinsfiles

Modern declarative pipelines (preferred pattern):
* [Jenkinsfile.e2e](../../validation/Jenkinsfile.e2e)
* [Jenkinsfile.individual.e2e](../../validation/Jenkinsfile.individual.e2e)

Legacy scripted pipelines (for compatibility reference only):
* [Jenkinsfile](../../validation/Jenkinsfile)
* [Jenkinsfile.harvester](../../validation/Jenkinsfile.harvester)

Upgrade pipelines:
* [Jenkinsfile.upgrade.e2e](../../validation/Jenkinsfile.upgrade.e2e)

Airgap pipelines:
* [Jenkinsfile.airgap-rke2-tests](../../validation/pipeline/Jenkinsfile.airgap-rke2-tests)
* [Jenkinsfile.setup.airgap.rke2](../../validation/pipeline/Jenkinsfile.setup.airgap.rke2)
* [Jenkinsfile.destroy.airgap.rke2](../../validation/pipeline/Jenkinsfile.destroy.airgap.rke2)

Recurring pipelines:
* [Jenkinsfile.recurring](../../validation/pipeline/Jenkinsfile.recurring)
* [Jenkinsfile.multibranch.recurring](../../validation/pipeline/Jenkinsfile.multibranch.recurring)

Rancher HA pipelines:
* [Jenkinsfile.ha.deploy](../../validation/pipeline/rancherha/Jenkinsfile.ha.deploy)

QA infra / Elemental pipelines:
* [Jenkinsfile.elemental.e2e](../../validation/pipeline/qainfra/Jenkinsfile.elemental.e2e)
* [Jenkinsfile.elemental.harvester.e2e](../../validation/pipeline/qainfra/Jenkinsfile.elemental.harvester.e2e)

## Checklist

### Shared library usage
* All pipelines must load the `qa-jenkins-library` shared library using the pattern:
  ```groovy
  def libraryBranch = env.QA_JENKINS_LIBRARY_BRANCH ?: 'main'
  library "qa-jenkins-library@${libraryBranch}"
  ```
* Infrastructure operations must use library abstractions (`make.runTarget`, `tofu.*`, `infrastructure.*`, `airgap.standardCheckout`) — never invoke raw `sh` commands for these directly in a Jenkinsfile.
* Test execution must use library helpers (`property.useWithProperties`) rather than ad-hoc credential or environment wiring.
* Common logic shared across multiple Jenkinsfiles must live in the shared library, not be duplicated inline.

### Pipeline structure
* New pipelines must use declarative `pipeline {}` syntax. Scripted `node {}` pipelines are legacy and should not be created.
* Pipeline logic in Jenkinsfiles must be minimal — orchestration only. Business logic belongs in shared library functions.
* Avoid unnecessary layers of abstraction within the Jenkinsfile itself.

### Parameters and configuration
* All configurable values (paths, images, timeouts, tags, credentials, regions, repo URLs) must be pipeline parameters or sourced from library helpers — never hardcoded.
* Use `?:` (Elvis operator) to provide safe defaults for all environment variable reads (e.g., `env.TIMEOUT ?: '60m'`).
* Input validation must be present for all public functions: required map keys must be checked and meaningful errors raised when missing.

### Error handling and cleanup
* Error handling must be explicit: use `error(message)` with clear messages.
* Cleanup logic (containers, workspaces, artifacts, infrastructure teardown) must execute on failure as well as success — use `post { always { } }` or `try/catch/finally` blocks.
* Infrastructure teardown must guard against double-cleanup using a flag (e.g., `env.INFRA_CLEANED == 'true'`).
* Teardown failures must be caught and logged without masking the original build failure.

### Credentials and secrets
* Credentials and sensitive data must be handled exclusively through Jenkins credentials bindings or library helpers.
* Secrets must never be logged, echoed, or hardcoded in any form.

### Workspace hygiene
* Workspace usage must be deterministic and isolated: clean checkout, predictable directory names, no cross-build contamination.
* Container and volume names must include `${JOB_NAME}` and `${BUILD_NUMBER}` to ensure uniqueness.

### Naming conventions
* Use centralized naming utilities from the shared library rather than ad-hoc string concatenation for job names, container names, and workspace names.
* Variable names must use `lowerCamelCase` in Groovy.

### Artifacts and results
* Test results and artifacts must always be archived in a standard way, even in partial-failure scenarios where results are available.
* Use `junit` and `archiveArtifacts` steps consistently across all pipelines.

### Readability
* Jenkinsfiles must include a top-level Groovy doc comment (`/** ... */`) describing the pipeline's purpose, what it does, and which shared library functions it consumes — following the pattern in `Jenkinsfile.e2e` and `Jenkinsfile.individual.e2e`.
* Inline comments must explain non-obvious decisions (e.g., why a `TODO` exists).

### Pinned versions
* Docker image and libs references must use pinned digests or explicit version tags — never `latest`.