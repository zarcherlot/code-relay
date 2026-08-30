---
name: codex-relay-verifier
description: Let a Codex host join a branch-scoped Codex Relay subscription and verify incoming tasks.
---

# Codex Relay verifier

The verifier is bound to one canonical repository URL and one remote ref. The local checkout is only the setup context; verification always runs in an isolated worktree at the task's `source_commit`.

## Join

When the user pastes a Codex Relay join link, preview repository, ref, task path, artifact policy, and requested permissions. Ask for confirmation, then use the plugin join action. Do not accept a link for a different repository/ref without explicit confirmation.

If the current Codex window has no repository, offer to clone the invited `repository` and `ref` into a user-confirmed empty directory, then associate that directory before starting the watcher. If it contains a different repository or branch, leave it untouched and offer to open/clone the invited project instead.

## Runtime behavior

The background watcher fetches the bound ref and materializes only new `tasks/<task_id>/task.md` files into its private inbox. It deduplicates by `repository + ref + task_id + source_commit`. A host-specific verifier adapter may be configured as `agent_command`; when present it is invoked for each new task. Otherwise the Codex session can inspect the inbox and use the bounded verifier runtime to create and publish `receipt.json`, `receipt.md`, and declared artifacts.

The model must not push code or alter the user's active worktree. The agent validates the receipt itself before publishing it.

## Natural language controls

Support “验证器状态”“暂停验证”“恢复验证”“重新执行 task-003” and “查看最近回执” through plugin tools. Failures and blocked commands always produce a receipt; never silently retry destructive or unapproved commands.
