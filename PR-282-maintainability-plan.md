# Maintainability & LOC Reduction Plan for Jenkins Airgap RKE2 Deployment (PR #282)

Last updated: 2025-10-30

## Goals and Success Criteria
- Reduce total LOC by 20–40% without losing functionality.
- Eliminate duplicated logic across Jenkins, Terraform/OpenTofu, Ansible, Docker, and Shell.
- Standardize configuration and coding patterns to improve readability and onboarding.
- Pass baseline linters: shellcheck/shfmt, tflint/terraform fmt/validate, ansible-lint, hadolint, Groovy CPS sanity.
- Produce a smaller, clearer Jenkins pipeline with reusable steps and deterministic artifacts for airgapped RKE2.

## Baseline and Metrics (run once before changes)
- Size/duplication:
  - cloc . (capture baseline LOC per language)
  - git ls-files | xargs -n1 -I{} bash -lc 'printf "%s\n" {}' > .baseline-files.txt (freeze current file list)
- Shell:
  - shellcheck -S style scripts/**/*.sh
  - shfmt -d -i 2 -ci scripts
- Terraform/OpenTofu:
  - tofu/terraform fmt -recursive -check
  - tflint --recursive
  - tofu/terraform validate (per workspace)
- Ansible:
  - ansible-lint -q
- Docker:
  - hadolint Dockerfile*
- Pipeline:
  - Jenkinsfile: note stages, repeated sh blocks, environment duplication.

Record results in PR description as “Baseline Metrics” to show improvement deltas.

## Target Architecture and Conventions
- Common shell library:
  - scripts/lib/common.sh containing: strict-mode, logging, retries/backoff, json/yaml helpers, file locking, trap/cleanup, env loading.
- Idempotency-first:
  - All scripts safe to re-run; Ansible tasks use modules over shell; Terraform uses for_each/count with data-driven inputs.
- Naming/layout:
  - scripts/ (entrypoints) and scripts/lib/ (shared), ansible/roles/*, terraform/modules/*, docker/ (images), config/ (inputs), ci/ (Jenkins shared steps if not using org-level shared library).

## Refactor Workstreams (apply in order, minimize diff per commit)

1) Centralize configuration
- Configuration is handled in the Jenkins UI

2) Shell consolidation and cleanup
- Create scripts/lib/common.sh with:
  - set -Eeuo pipefail; IFS=$'\n\t'
  - log() with levels; die(); retry(cmd, attempts, backoff)
  - require_env VAR [VAR...]; load_env scripts/.env
  - json_get/jq wrapper; yaml_get/yq wrapper
- Update entrypoint scripts to:
  - source "$(dirname "$0")/lib/common.sh"; load_env
  - Replace duplicate curl/jq loops with retry and helpers.
  - Use getopts for flags; exit codes > 0 only on irrecoverable failures.
- Run shellcheck fixes and shfmt formatting.

3) Jenkins Pipeline simplification
- Extract repeated sh steps to reusable functions:
  - If using a shared library: ci/vars/rke2.groovy with steps: airgapMirror(), planInfra(), applyInfra(), bootstrapRKE2(), runValidation(), teardown().
  - If not: ci/pipeline.groovy loaded via load 'ci/pipeline.groovy'.
- Jenkinsfile becomes a thin orchestrator:
  - Stages call the shared steps with params read from config/params.yaml (use libraryResource or readYaml).
  - Standardize environment (withCredentials, tool versions, workspace paths).
  - Add options { durabilityHint('PERFORMANCE_OPTIMIZED'); disableConcurrentBuilds() }
- Add post actions for archiving artifacts: manifests, images.txt, tf plans, kubeconfigs.

4) Terraform/OpenTofu moduleization
- Terraform is defined in a separate repo

5) Ansible role hygiene
- This will be handled in a separate repo

6) Docker images (if applicable)
- Multi-stage builds; pin base images; shared base where practical; .dockerignore.
- Add LABEL org.opencontainers.image.* metadata; hadolint clean.



## Step-by-Step Plan for PR #282

Phase 0: Metrics and scaffolding (small commits)
- Add config/params.yaml with current values; add Makefile target generate.
- Add scripts/lib/common.sh and update one or two scripts as example.
- Add ci/pipeline.groovy or shared library vars/rke2.groovy with one extracted step; wire Jenkinsfile to use it.
- Run all linters; commit baseline and initial improvements.

Phase 1: Shell refactor (highest duplication wins)
- Replace repeated HTTP/download/parsing code with common.sh helpers across scripts.
- Standardize argument parsing and logging; ensure strict mode and traps.
- Remove dead code, inline constants -> params.

Phase 2: Jenkins pipeline thinning
- Move remaining stage bodies into library functions.
- Centralize environment setup (tool paths, creds, registry) in one step.
- Add artifact archiving and timestamps; parallelize independent stages (e.g., image mirror vs infra plan if feasible).

Phase 3: Terraform/OpenTofu modules
- handled in another Repo

Phase 4: Ansible roles
- handle in another repo

Phase 5: Docker/Artifacts polish
- Normalize Dockerfiles; multi-stage; labels; hadolint clean.
- Implement artifacts/ structure and retention policy.

Phase 6: Verification and docs
- Smoke tests script: scripts/validate_cluster.sh using kubectl wait and basic probes.
- Update README: how to run generate, pipeline overview, airgap workflow, troubleshooting.

## Tooling and Automation
- Pre-commit (optional but recommended, minimal footprint):
  - terraform_fmt, tflint, shellcheck, shfmt, ansible-lint, hadolint.
- Makefile shortcuts:
  - make generate, make lint, make plan, make apply, make destroy, make validate.

## Risk Management and Rollback
- Keep changes in small, reviewable commits per phase; feature-flag new library calls via env var ENABLE_LIB=1 for quick rollback.
- Maintain compatibility shims: old script entrypoints source new functions.
- Promote in Jenkins with a canary job first; keep previous successful build artifacts available.

## Deliverables Checklist for PR #282
- config/params.yaml and generation wiring.
- scripts/lib/common.sh and updated scripts sourcing it.
- Slim Jenkinsfile + ci/pipeline.groovy or shared library.
- terraform/modules/* with for_each and variable validations; lint clean.
- ansible/roles/* with templates and idempotency; lint clean.
- Dockerfiles normalized; hadolint clean.
- Updated README and a short MIGRATION.md if necessary.

## Coding Standards (quick reference)
- Shell: set -Eeuo pipefail; shellcheck disable lines justified; shfmt -i 2 -ci; use getopts; functions + early returns.
- Groovy (Jenkins): steps in shared lib; no long sh scripts inline; readYaml for params; withCredentials blocks localized.
- Terraform: variables lower_snake_case; outputs for handoff; use for_each/dynamic; fmt/tflint clean.
- Ansible: roles, not include_tasks where import_tasks fits; modules over shell; tags and idempotency.
- Docker: pin versions; multi-stage; labels; minimal layers.

---

Execution tip: Focus each commit on one refactor dimension and keep the Jenkinsfile running green at every step; measure LOC and linter deltas after each phase to quantify improvements.