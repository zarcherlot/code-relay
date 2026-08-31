<h1 align="center">
  <img src="assets/icon.png" alt="" width="36" height="36" style="vertical-align: middle;">
  Code Relay
</h1>

<p align="center">
  <a href="README.md">English</a> · <a href="README.zh-CN.md">中文版</a>
</p>

<p align="center"><sub>build, verify, and relay</sub></p>

<p align="center">
  <img src="assets/overview.png" alt="Code Relay workflow" width="836">
</p>

> **Code Relay lets Codex develop on one machine and prove the result on another.**

Relay is a Codex plugin for cross-machine development and verification. AI on the **Dev Host** implements the request; the **Target Host** runs the real validation; a structured receipt comes back and can trigger the next repair iteration.

## How it works

```text
Describe a task in Codex
        ↓
Dev Host → Relay task → Target Host
        ↓                 ↓
     develop          verify in the real environment
        ←────── Receipt / evidence ──────
```

Relay binds every task to a repository, branch, and source commit, so the Target Host verifies the code you actually intended to ship. Pass, fail, and blocked results remain auditable in Git.

## When Relay is useful

- **Different environments** — develop on Windows or macOS, verify on Linux, a private network, or a production-like host.
- **Special hardware** — send model code to a CUDA/GPU machine, or verify a camera, serial device, or sensor where it is connected.
- **Real integrations** — exercise payment callbacks, queues, databases, browsers, or internal services that cannot be reproduced locally.
- **AI-driven iteration** — return concrete expected/actual results so Codex can repair, republish, and retry.

## Quick start

1. Install **Code Relay** from the Codex plugin UI.
2. On the Dev Host, open a project and say:

   > Enable Code Relay for the current project and branch, then generate a Target Host join link.

3. On the Target Host, install the same plugin and paste the link into Codex.
4. The Target Host joins the approved repository and branch, runs tasks on its `codex-b` GitHub Actions runner, and publishes a receipt.

Users do not need to install Python, Go, or a separate Relay runtime for the packaged plugin. See [USER_GUIDE.md](USER_GUIDE.md) for the complete journey and recovery steps.

### Install from this repository (local desktop)

The repository includes a repo-scoped marketplace at
`.agents/plugins/marketplace.json`. To install from a source checkout, build the
platform package once from the repository root:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass `
  -File .\scripts\package-plugin.ps1 `
  -Output .\dist\plugin
```

This creates the native agent and packaged `.mcp.json` under `dist/plugin`. Go is
needed only for this source build; the installed package does not need Go at
runtime. If `dist/plugin` was supplied as a prebuilt package, skip the command.

Restart the ChatGPT desktop app, open `Plugins Directory`, choose
`Code Relay (Local)` → `Code Relay` → `Install`, and start a new chat to test it.
Keep the default `dist/plugin` output so the checked-in marketplace path remains
valid. No manual marketplace JSON or `codex plugin marketplace add` command is
required.

## Core capabilities

- Branch-scoped project binding and short-lived join links.
- Exact source-commit verification in an isolated worktree.
- GitHub Actions integration with a `codex-b` self-hosted runner.
- Safe, allowlisted validation commands with bounded output and timeouts.
- Structured `receipt.json` plus a human-readable receipt for pass, fail, and blocked runs.

## Development

The repository targets Go 1.26+:

```powershell
go test ./...
go vet ./...
go build ./cmd/code-relay-agent
./scripts/validate-contracts.ps1
./scripts/smoke-e2e.ps1
```

Build the bundled agent for the current platform with `./scripts/build-agent.ps1`. The CLI is a developer/CI fallback; normal users install through Codex.

## Documentation

- [中文 README](README.zh-CN.md)
- [User guide](USER_GUIDE.md)
- [Deployment and runbook](deploy/RUNBOOK.md)
- [Security](SECURITY.md)
- [Contributing](CONTRIBUTING.md)

## License

Code Relay is released under the [MIT License](LICENSE).
