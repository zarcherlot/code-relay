# Changelog

All notable changes to Code Relay are recorded here. The project is currently in the MVP stage.

## [Unreleased]

### Added

- Added the `code-relay-mcp` npm distribution with a zero-dependency launcher
  that downloads and verifies the matching native Code Relay release artifact.
- Added an agent-readable `install.md` and an idempotent installer for Codex,
  Claude Code, Cursor, VS Code, and generic MCP clients.
- Added official MCP Registry metadata and tag-driven npm/Registry publication
  automation.

### Changed

- Renamed the GitHub Actions checkpoint runner label from `codex-b` to
  `code-relay-checkpoint`; existing runners must be relabelled before upgrading.

## [3.0.0] - 2026-08-31

### Breaking

- Replaced the public `task` contract with `runbook`, including storage paths,
  schemas, receipt bindings, MCP arguments, CLI commands, and workflow inputs.
- Raised branch binding and invitation protocols to v2 so 3.0 rejects stale
  `task`-based project state and join links.
- Renamed the target-side `verify` Skill and binding role to `checkpoint`; the
  only explicit Skill invocations are now `$code-relay:relay` and
  `$code-relay:checkpoint`.
- Removed the prior `task` and `verifier` names without compatibility aliases.

### Documentation

- Clarified the product artifact model: Relay sends a Runbook to a Checkpoint,
  and the Checkpoint returns a Receipt.
- Updated the overview illustration to label the outbound Runbook, target-side
  Checkpoint, and returned Receipt separately.

### Reliability and release engineering

- Added hosted MCP mode with OAuth 2.1/PKCE, encrypted stateless sessions,
  GitHub App installation authorization, repository/branch access checks, and
  GitHub Contents/Actions runbook and receipt flow.
- Added hosted MCP audit test matrix and JSON request audit logging with secret
  redaction boundaries.
- Added contract validation for schemas, fixtures, plugin metadata, and MCP configuration.
- Added cross-platform local end-to-end smoke coverage for passed and failed verification paths.
- Added release artifact manifest/checksum verification and a repeatable release/rollback checklist.
- Tightened Git child-process environment isolation, managed-directory permissions, daemon listener policy, and worktree pruning.
- Added checkpoint operations runbook and CODEOWNERS governance.

## [2.0.0] - 2026-08-30

### Breaking

- Removed all legacy naming and compatibility paths; only `code-relay`, `.code-relay`, and `code-relay-agent` remain.
- Standardized the product on the Go runtime and a packaged native MCP executable for Linux, macOS, and Windows.

### Security and reliability

- Added root-confined path validation, symlink-component rejection, remote credential sanitization, durable cross-platform atomic replacement, bounded process output, and child-process-tree timeouts.
- Removed shell interpreters and inline eval/exec escape modes from validation policy.
- Added project-scoped queue/receipt locks, invalid-receipt recovery, explicit worktree failure receipts, and fail-closed one-time invitation consumption.
- Hardened MCP JSON-RPC validation and recovery, pinned GitHub Actions by commit, and added checksums, SBOM metadata, provenance, and package smoke tests.
- Raised the developer baseline to Go 1.26 and standardized CI/release builds on Go 1.27 for current macOS linker compatibility.

### Testing

- Added cross-platform unit, concurrent state, malformed protocol, invitation race, and publish-to-analysis end-to-end coverage.

## [1.0.0] - 2026-08-30

### Breaking

- Consolidated the product, Go agent, CLI, metadata directory, invitation URI, and secret environment variable under the Code Relay naming scheme.
- Removed deprecated package namespaces, launchers, migration commands, and legacy protocol formats.
- Added the Go `code-relay-agent` runtime for Linux, macOS (Intel and Apple Silicon), and Windows.

## [0.3.0] - 2026-08-30

### Security

- Added strict runbook/receipt size, field, runbook ID, and commit SHA validation.
- Validation commands now run without a shell, with executable allowlisting, bounded output, timeout process-tree termination, secret environment scrubbing, and worktree scope checks.
- Added optional HMAC integrity for invitations through `CODE_RELAY_INVITE_SECRET`.
- Added atomic state/receipt writes, private config file permissions where supported, watcher binding checks, webhook body limits, and safer remote runbook path handling.
- Hardened the GitHub Actions workflow with non-persistent checkout credentials and deterministic worktree cleanup.

### Removed

- Removed historical Logo and architecture concept drafts from the release tree.

## [0.2.0] - 2026-08-30

### Added

- Formal Codex plugin manifest under `.codex-plugin/plugin.json`.
- Orchestrator and checkpoint Skills with plugin-local MCP tools.
- Branch-scoped project binding using canonical repository and remote ref.
- Short-lived checkpoint invitation links.
- Checkpoint-side empty-workspace clone, project association, and watcher bootstrap.
- Private runbook inbox, remote-ref polling, deduplication, receipt validation, and self-hosted runner workflow.
- End-to-end tests for binding, invitations, cloning, watcher lifecycle, and safe execution.

### Changed

- User-facing installation is plugin-first; the CLI remains a developer/CI fallback.
- Product and runtime naming is unified as Code Relay.

### Known limitations

- Invitations are bearer links and are not yet signed or revocable.
- Automatic checkpoint-agent startup is exposed through a host adapter hook; the bundled watcher itself does not assume a particular Codex CLI integration.
