# Contributing to Code Relay

Thanks for helping improve Code Relay. The project is intentionally small: changes should preserve the branch-scoped protocol and keep the coding-agent conversation as the primary user interface.

## Before opening a change

1. Read [README.md](README.md), [USER_GUIDE.md](USER_GUIDE.md), and the relevant Skill under `skills/`.
2. Keep user-facing setup plugin-first. Do not introduce a required language runtime installation step.
3. Keep repository and remote ref explicit in new binding or watcher behavior.
4. Do not add secrets, tokens, generated invitations, watcher state, or local receipts to commits.

## Local checks

Run the complete test and compile checks from the repository root:

```powershell
go test ./...
go vet ./...
go build ./cmd/code-relay-agent
npm ci
npm test
npm run verify
./scripts/validate-contracts.ps1
./scripts/smoke-e2e.ps1
./scripts/package-plugin.ps1 -Output dist/plugin-smoke
git diff --check
```

CI additionally runs the race detector on Linux and checks the npm launcher on
Windows, macOS, and Linux. Node.js 18+ is required for npm distribution work.
The packaging command builds the native executable, checks its version, and
parses the generated `.mcp.json`. When changing a Skill, also verify its
frontmatter before submitting.

## Pull requests

- Explain the user-visible behavior and the security implications.
- Add or update a focused test for protocol, binding, watcher, or receipt changes.
- Keep commits narrow and document user-visible protocol or storage changes.
- Never include real invitation links, GitHub tokens, runner credentials, private repositories, or customer data.

## Design principles

- One binding means one canonical repository and one remote branch.
- The Checkpoint Host must never silently overwrite an existing workspace or active worktree.
- Runbooks and receipts are immutable, source-commit-bound records.
- Failed and blocked validation must produce an explicit receipt.
- Dangerous commands require a deliberate product decision; they are not enabled by convenience.
