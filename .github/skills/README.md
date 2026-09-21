# GitHub Copilot CLI — Skills

This folder contains custom skills for the `rancher/tests` repository. Skills are
Markdown files (`SKILL.md`) that Copilot auto-discovers from `.github/skills/<name>/`
— no manifest or index file is required, the folder name and frontmatter are enough.

Skills are:

- Automatically considered by Copilot when their `description` matches the current task, or
- Manually invoked via `/skills`, or
- Pulled into the context of a built-in agent (e.g. the `code-review` task agent used
  by `/review` can use the `code-review` skill below when reviewing this repo)

## Listing available skills

Inside the CLI, run:

```
/skills
```

## Available Skills

### `code-review` — rancher/tests Code Review

**File:** [`code-review/SKILL.md`](./code-review/SKILL.md)

Repository-specific review checklist for Go test files and Jenkinsfiles: build tags,
suite/test naming, `Dynamic`/static test pairing, session cleanup, helper conventions,
and Jenkinsfile shared-library/security/parameterization rules.

**When to use:** Reviewing a PR or diff in this repository — including via the built-in
`code-review` task agent (e.g. `/review`) or standalone through `/skills`.

### `schema` — rancher/tests PIT Schema Generator

**File:** [`schema/SKILL.md`](./schema/SKILL.md)

Creates or updates `schemas/pit_schemas.yaml` files for PIT (Platform Interoperability
Testing) test packages, based on a package's `Test*` functions.

**When to use:** After writing or modifying `*_test.go` files in a PIT-tagged package
(e.g. `pit.daily`, `pit.weekly`) to keep the Qase schema file in sync.

## Additional Resources

- [Repository Copilot instructions](../copilot-instructions.md)
- [TAG_GUIDE.md](../../TAG_GUIDE.md) — build tag conventions
- [GitHub Copilot CLI documentation](https://docs.github.com/en/copilot/how-tos/use-copilot-agents/copilot-cli/using-copilot-cli) — installation and usage instructions

