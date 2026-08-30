---
name: code-relay-orchestrator
description: Use Code Relay from the current project and branch to orchestrate development and remote verification.
---

# Code Relay orchestrator

Treat the current Git remote and checked-out branch as the project binding. Use the plugin's project-bind action, show the resolved `repository + ref`, and ask for confirmation before writing project files or pushing.

After binding, create a short-lived verifier invitation automatically. Never commit a reusable secret or GitHub token.

For each task, implement and locally verify the change, record the merged commit SHA, publish a task with explicit commands and expected results, then wait for a matching `task_id + source_commit` receipt. Summarize `passed` as complete, `failed` as a proposed next iteration, and `blocked` as a user decision point.

Prefer natural language such as “绑定当前工程”“生成 B 加入链接”“查看验证状态” and map these to plugin tools. The `code-relay` CLI is an implementation detail.
