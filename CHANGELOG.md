# Changelog

All notable changes to Code Relay are recorded here. The project is currently in the MVP stage.

## [1.0.0] - 2026-08-30

### Breaking

- Consolidated the product, Python package, CLI, metadata directory, invitation URI, and secret environment variable under the Code Relay naming scheme.
- Removed deprecated package namespaces, launchers, migration commands, and legacy protocol formats.
- Added the Go `code-relay-agent` runtime for Linux and Windows.

## [0.3.0] - 2026-08-30

### Security

- Added strict task/receipt size, field, task ID, and commit SHA validation.
- Validation commands now run without a shell, with executable allowlisting, bounded output, timeout process-tree termination, secret environment scrubbing, and worktree scope checks.
- Added optional HMAC integrity for invitations through `CODE_RELAY_INVITE_SECRET`.
- Added atomic state/receipt writes, private config file permissions where supported, watcher binding checks, webhook body limits, and safer remote task path handling.
- Hardened the GitHub Actions workflow with non-persistent checkout credentials and deterministic worktree cleanup.

### Removed

- Removed historical Logo and architecture concept drafts from the release tree.

## [0.2.0] - 2026-08-30

### Added

- Formal Codex plugin manifest under `.codex-plugin/plugin.json`.
- Orchestrator and verifier Skills with plugin-local MCP tools.
- Branch-scoped project binding using canonical repository and remote ref.
- Short-lived verifier invitation links.
- B-side empty-workspace clone, project association, and watcher bootstrap.
- Private task inbox, remote-ref polling, deduplication, receipt validation, and self-hosted runner workflow.
- End-to-end tests for binding, invitations, cloning, watcher lifecycle, and safe execution.

### Changed

- User-facing installation is plugin-first; the CLI remains a developer/CI fallback.
- Product and runtime naming is unified as Code Relay.

### Known limitations

- Invitations are bearer links and are not yet signed or revocable.
- Automatic verifier-agent startup is exposed through a host adapter hook; the bundled watcher itself does not assume a particular Codex CLI integration.
