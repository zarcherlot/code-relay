# Code Relay release checklist

This checklist is for the incompatible 2.x line. It does not introduce a
runtime feature or compatibility layer.

## Candidate gate

- [ ] Worktree is clean and the intended commit is pushed to `main`.
- [ ] `go test ./...`, `go vet ./...`, and `git diff --check` pass with Go 1.26+.
- [ ] `scripts/validate-contracts.ps1` passes.
- [ ] `scripts/smoke-e2e.ps1` passes on Windows, macOS, and Linux.
- [ ] `scripts/build-agent.ps1` or `scripts/build-agent.sh` creates all five target binaries.
- [ ] `scripts/verify-release.ps1 -Path <dist> -Version <version>` verifies checksums and the manifest.
- [ ] The packaged plugin starts the native `code-relay-agent mcp-stdio` binary.

## GitHub release

1. Confirm the version in `cmd/code-relay-agent/main.go` and
   `.codex-plugin/plugin.json`.
2. Create and push an annotated tag, for example `v2.0.0`.
3. Confirm the release workflow publishes five binaries, `release.json`,
   `sbom.cdx.json`, and `SHA256SUMS`.
4. Download one artifact on each supported operating system and run
   `code-relay-agent version` and `code-relay-agent doctor --root <project>`.
5. Retain the workflow run URL and source commit in the release notes.

## Rollback

- Pause the checkpoint workflow or disable the affected runner label.
- Restore the previous known-good binary and keep the project binding intact.
- Do not delete runbooks or receipts; re-run only after the source commit and
  receipt state have been reviewed.
- Record the failed release tag, workflow run, and recovery commit.
