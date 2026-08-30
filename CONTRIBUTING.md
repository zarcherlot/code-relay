# Contributing to Code Relay

Thanks for helping improve Code Relay. The project is intentionally small: changes should preserve the branch-scoped protocol and keep the Codex conversation as the primary user interface.

## Before opening a change

1. Read [README.md](README.md), [USER_GUIDE.md](USER_GUIDE.md), and the relevant Skill under `skills/`.
2. Keep user-facing setup plugin-first. Do not introduce a required language runtime installation step.
3. Keep repository and remote ref explicit in new binding or watcher behavior.
4. Do not add secrets, tokens, generated invitations, watcher state, or local receipts to commits.

## Local checks

Run the complete test and compile checks from the repository root:

```powershell
go test -race ./...
go vet ./...
go build ./cmd/code-relay-agent
```

When changing `.codex-plugin/plugin.json`, `.mcp.json`, or a Skill, also verify JSON and frontmatter before submitting.

## Pull requests

- Explain the user-visible behavior and the security implications.
- Add or update a focused test for protocol, binding, watcher, or receipt changes.
- Keep commits narrow and document user-visible protocol or storage changes.
- Never include real invitation links, GitHub tokens, runner credentials, private repositories, or customer data.

## Design principles

- One binding means one canonical repository and one remote branch.
- B must never silently overwrite an existing workspace or active worktree.
- Tasks and receipts are immutable, source-commit-bound records.
- Failed and blocked validation must produce an explicit receipt.
- Dangerous commands require a deliberate product decision; they are not enabled by convenience.
