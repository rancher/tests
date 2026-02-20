# GitHub Copilot CLI — Custom Agents

This folder contains custom agents for the `rancher/tests` repository. These agents are invoked via the GitHub Copilot CLI and provide specialized, context-aware assistance for tasks such as test code review and PIT schema generation.

---

## Installing GitHub Copilot CLI

### Install

**macOS / Linux (Homebrew):**
```bash
brew install copilot-cli
```

**Windows (WinGet):**
```powershell
winget install GitHub.Copilot
```

**npm (all platforms):**
```bash
npm install -g @github/copilot
```

**Install script (macOS / Linux):**
```bash
curl -fsSL https://gh.io/copilot-install | bash
```

---

## Authenticating

Launch the CLI and log in:

```bash
copilot login
```

## Launching the CLI

Run `copilot init` from the repository root (or any subdirectory):

```bash
cd /path/to/rancher/tests
copilot init
```

Custom agents and repository instructions are loaded automatically from:

- `.github/copilot-instructions.md`
- `.github/agents/*.agent.md`

---

## Using Custom Agents

### Listing available agents

Inside the CLI, run:

```
/agent
```

---

## Available Agents

### `pit.crew.schema` — PIT Schema Generator

**File:** [`pit.crew.schema.agent.md`](./pit.crew.schema.agent.md)

Generates or updates `schemas/pit_schemas.yaml` files for PIT (Platform Interoperability Testing) test packages.

**When to use:** After writing or modifying `*_test.go` files in a PIT-tagged package (e.g. `pit.daily`, `pit.weekly`), run this agent to keep the Qase schema file in sync.

**Example prompt:**
```
copilot --agent=pit.crew.schema --allow-tool write -p "Create the pit_schemas.yaml for @validation/networking/connectivity/"
```

## Additional Resources

- [GitHub Copilot CLI documentation](https://docs.github.com/copilot/concepts/agents/about-copilot-cli)
- [Repository Copilot instructions](../copilot-instructions.md)
- [TAG_GUIDE.md](../../TAG_GUIDE.md) — build tag conventions
