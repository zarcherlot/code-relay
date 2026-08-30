---
name: codex-relay-orchestrator
description: Use Codex Relay from the current project and branch to orchestrate development and remote verification.
---

# Codex Relay orchestrator

When this skill is active, treat the current Git remote and checked-out branch as the project binding. Do not ask the user to install a CLI or edit task Markdown by hand.

## First use in a project

Use the plugin's project-bind action. It must inspect `git remote get-url origin` and `git branch --show-current`, show the resolved `repository + ref`, and ask for confirmation before writing project files or pushing.

After binding, create a short-lived verifier invitation automatically and show it to the user. Never commit a reusable secret or GitHub token.

## Per-task loop

1. Implement the user's request and run local checks.
2. Create/push a PR and wait for merge. Record the exact merged commit SHA.
3. Generate a task from the natural-language request and the merged SHA. Include explicit validation commands, expected results, artifact paths, timeout, and the parent task when iterating.
4. Commit and push the task to the bound remote ref.
5. Watch for a receipt. A matching `task_id + source_commit` is required; never infer success from a green workflow alone.
6. Summarize `passed` as complete, `failed` as a proposed next iteration, and `blocked` as a user decision point.

## User-facing commands

Prefer natural language such as “绑定当前工程”“生成 B 加入链接”“查看验证状态”“重新执行 task-003”. Map these to plugin tools; the underlying relay CLI is an implementation detail.

