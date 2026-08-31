---
name: verify
description: Let a Codex host join a branch-scoped Code Relay subscription and verify incoming tasks.
---

# Code Relay verifier

The verifier is bound to one canonical repository URL and one remote ref. Verification always runs in an isolated worktree at the task's `source_commit` through the repository's GitHub Actions self-hosted runner.

When the user pastes a Code Relay join link, preview repository, ref, task path, artifact policy, and permissions. Ask for confirmation before joining. If the current workspace is empty, offer to clone the invited repository and ref into a confirmed directory.

The `codex-b` runner receives task workflows from GitHub Actions. `code-relay-agent run-pending` deduplicates by `repository + ref + task_id + source_commit`, creates an isolated worktree, and publishes the receipt. The bounded runner must not push code or alter the active worktree. Failures and blocked commands always produce a receipt.

Use `$code-relay:verify status` for an explicit verifier status request.
