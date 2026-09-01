# Code Relay launch guide

## Listing metadata

- Name: Code Relay
- Tagline: Branch-scoped remote verification for coding agents
- Description: Code Relay lets coding agents develop on one machine and verify the exact branch on another host. It runs branch-scoped runbooks, remote checkpoints, and auditable verification receipts for Codex, Claude Code, Cursor, VS Code, and other MCP-capable agents.
- Category: Developer Tools
- Pricing: Free
- Use cases: Cross-machine development, CI/CD verification, remote testing, hardware and integration validation
- Tags: coding-agents, remote-verification, devops, runbooks, checkpoints, testing
- Documentation: https://github.com/zarcherlot/code-relay#readme

## Source and installation

- Repository: https://github.com/zarcherlot/code-relay
- npm package: code-relay-mcp
- Transport: stdio
- Runtime: Node.js 18+ launcher with a bundled Go implementation
- Install: `npx -y code-relay-mcp@3.1.0`
- Start: `npx -y code-relay-mcp@3.1.0`

The package also supports client setup with `npx -y code-relay-mcp@latest install --client <client>`, where `<client>` is `codex`, `claude-code`, `cursor`, `vscode`, or `generic`.

## MCP protocol and tools

Code Relay implements MCP JSON-RPC directly in Go. The stdio server supports
`initialize`, `ping`, `tools/list`, and `tools/call`. The exposed tools are:

- `bind_project`
- `create_checkpoint_invite`
- `join_checkpoint`
- `watcher_status`
- `stop_watcher`
- `doctor`
- `publish_runbook`
- `status`
- `fetch_receipt`
- `analyze`

Implementation details are in [`internal/relay/mcp.go`](internal/relay/mcp.go).

## Validation

The server is intended for local stdio use. A reviewer can verify it with:

```sh
npx -y code-relay-mcp@3.1.0 --version
npx -y code-relay-mcp@3.1.0 --help
```
