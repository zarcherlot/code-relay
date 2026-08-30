# Codex Relay

![Codex Relay Logo](assets/icon.png)

The Codex Relay logo is the red wax-seal coding-agent mark in [`assets/icon.png`](assets/icon.png). The earlier SVG concepts remain in `assets/` as historical drafts and are not used by the product.

Codex Relay is an open-source Codex plugin for a branch-scoped development and verification loop between two hosts:

- **A / orchestrator** develops in Codex, publishes a task, and analyzes the result.
- **B / verifier** joins a specific repository and remote branch, validates the requested commit, and publishes a structured receipt.
- **Git** is the shared transport for code, tasks, receipts, and audit history. No database or message queue is required for the MVP.

The user-facing entry point is the Codex plugin. The bundled MCP bridge, watcher, and CLI are runtime components used by the plugin and CI.

## What the MVP provides

- Branch-scoped binding: `repository + refs/heads/<branch>`.
- A natural-language project bind action that creates the B invitation automatically.
- A short-lived `codex-relay://join/...` invitation with repository/ref preview.
- Safe B bootstrap: if the current Codex window has no repository, an approved empty workspace is cloned and associated automatically.
- A background watcher that fetches only the subscribed ref and materializes new tasks into a private inbox without overwriting the active worktree.
- Idempotent task publication, source-commit binding, receipt validation, command allowlisting, timeout handling, and failure/blocked receipts.
- GitHub Actions support for a `codex-b` self-hosted runner.

## Install from Codex

Install **Codex Relay** from the Codex plugin UI. Users do not need to run `pip install`, `conda install`, or a separate relay installer.

### A: bind the current project

Open the project in Codex and say:

> 为当前工程当前分支启用 Codex Relay，并生成 B 验证端加入链接。

After showing the resolved remote repository and branch, the plugin writes `.codex-relay/project.json`, adds the project integration assets, pushes the binding, and returns a short-lived invitation link.

### B: join and verify

Install the same plugin on B and paste the invitation link into the Codex window. After confirmation:

1. An empty/unassociated workspace is cloned to the invited repository and branch.
2. A verifier binding is written and the watcher starts automatically.
3. New tasks are fetched from the bound remote ref and placed in the private inbox.
4. The verifier runtime or a configured host adapter executes the declared validation plan and publishes a receipt.

If the current directory is non-empty or belongs to another project, Codex Relay leaves it untouched and asks for a separate target directory.

The complete user journey is documented in [USER_GUIDE.md](USER_GUIDE.md).

## Repository layout

```text
.codex-plugin/plugin.json       # formal Codex plugin manifest
.mcp.json                       # plugin-local MCP server
skills/                         # orchestrator and verifier Skills
codex_relay/                    # public Python facade
codex_mate/                     # compatibility runtime namespace
schemas/                        # task and receipt JSON Schemas
templates/                      # task and receipt Markdown templates
.github/workflows/              # optional B self-hosted runner workflow
tests/                          # local MVP and end-to-end tests
```

## Developer validation

The repository is dependency-light and targets Python 3.10+:

```powershell
python -m unittest discover -s tests -v
python -m compileall -q codex_mate codex_relay tests scripts
```

The CLI is a developer/CI fallback, not the normal installation path:

```powershell
python -m codex_relay.relay --root . publish --file examples/task-001.md --no-git
python -m codex_relay.relay --root . run-task task-001
python -m codex_relay.relay --root . status --json
```

## Security and operating boundaries

Codex Relay is an MVP. Validation commands are allowlisted and destructive command patterns are blocked, but users must still review task content and runner permissions. Invitation links are short-lived bearer links; production deployments should add signing, revocation, and centralized audit policy before treating them as a complete authorization system.

Read [SECURITY.md](SECURITY.md) before exposing a runner or webhook, [CONTRIBUTING.md](CONTRIBUTING.md) before submitting changes, and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) before participating.

## Project status

The current release is an MVP reference implementation. The protocol and directory layout are intentionally small so that a team can inspect, fork, and replace individual runtime components.

## License

Codex Relay is released under the [MIT License](LICENSE).
