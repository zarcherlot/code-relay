<h1 align="center">
  <img src="assets/icon.png" alt="" width="36" height="36" style="vertical-align: middle;">
  Code Relay
</h1>

<p align="center">
  <a href="README.md">English</a> · <a href="README.zh-CN.md">Chinese</a>
</p>

<h2 align="center">
  Build. Prove. Relay.
</h2>

> **Code Relay lets your coding agent develop on one machine and prove the result on another.**

Relay is an MCP-based coding-agent integration for cross-machine development and
verification. AI on the Dev Host implements the request; the Target Host runs
the real validation; a structured receipt comes back to guide the next repair
iteration. It works with Codex, Claude Code, Cursor, VS Code, and other
MCP-capable coding agents.

## When Relay is useful

- **Different environments** — develop on Windows or macOS, verify on Linux, a private network, or a production-like host.
- **Special hardware** — send model code to a CUDA/GPU machine, or verify a camera, serial device, or sensor where it is connected.
- **Real integrations** — exercise payment callbacks, queues, databases, browsers, or internal services that cannot be reproduced locally.
- **AI-driven iteration** — return concrete expected/actual results so the coding agent can repair, republish, and retry.

## What Relay provides

- Branch-scoped project binding and short-lived join links.
- Exact source-commit verification in an isolated worktree.
- Checkpoint execution through a `code-relay-checkpoint` GitHub Actions self-hosted runner.
- Safe, allowlisted validation commands with bounded output and timeouts.
- Structured `receipt.json` and human-readable receipts for passed, failed, and blocked runs.

## Quick start

### AI client installation

Let your coding agent read and follow the instructions:

```text
https://raw.githubusercontent.com/zarcherlot/code-relay/main/install.md
```

### npm fallback

```sh
npx -y code-relay-mcp@latest install --client codex
```

Replace `codex` with `claude-code`, `cursor`, `vscode`, or `generic` as needed.

## MCP server details

Code Relay is a local MCP server that communicates over **stdio**. The npm
package starts the server with:

```sh
npx -y code-relay-mcp@3.1.0
```

The server implements the MCP JSON-RPC surface directly in Go (without a
third-party MCP SDK). It supports `initialize`, `ping`, `tools/list`, and
`tools/call`, and exposes these tools:

| Tool | Purpose |
| --- | --- |
| `bind_project` | Bind a repository and branch to a relay role. |
| `create_checkpoint_invite` | Create a short-lived checkpoint join link. |
| `join_checkpoint` | Join a branch-scoped checkpoint subscription. |
| `watcher_status` | Read local watcher state. |
| `stop_watcher` | Stop the local watcher. |
| `doctor` | Run non-mutating local health checks. |
| `publish_runbook` | Validate and publish a runbook. |
| `status` | Read runbook and receipt status. |
| `fetch_receipt` | Load and validate a runbook receipt. |
| `analyze` | Analyze a receipt and determine the next state. |

The implementation and transport loop are in [`internal/relay/mcp.go`](internal/relay/mcp.go).

## How it works

<p align="center">
  <img src="assets/overview.png" alt="Dev Host sends a runbook through Code Relay to a Target Host; the Target Host runs a checkpoint and returns a receipt with evidence" width="836">
</p>

the Dev Host sends a repository- and branch-bound runbook
through Relay; the Target Host checks the exact source commit in its real
environment and returns a Receipt recording the Checkpoint result.

Every runbook is bound to a repository, branch, and source commit. The Target
Host acts as the Checkpoint, and the resulting receipt records passed, failed,
or blocked verification for the next iteration.

## Documentation

- [Chinese README](README.zh-CN.md)
- [User guide](USER_GUIDE.md)
- [AI-client installation guide](install.md)
- [Local desktop install runbook](deploy/LOCAL-INSTALL-RUNBOOK.md)
- [Deployment and runbook](deploy/RUNBOOK.md)
- [Security](SECURITY.md)
- [Contributing](CONTRIBUTING.md)

## License

Code Relay is released under the [MIT License](LICENSE).
