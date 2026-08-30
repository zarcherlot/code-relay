# Changelog

All notable changes to Codex Relay are recorded here. The project is currently in the MVP stage.

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
- Product and runtime naming is unified as Codex Relay.

### Known limitations

- Invitations are bearer links and are not yet signed or revocable.
- Automatic verifier-agent startup is exposed through a host adapter hook; the bundled watcher itself does not assume a particular Codex CLI integration.
