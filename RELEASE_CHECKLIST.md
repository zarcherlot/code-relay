# Code Relay release checklist

This checklist covers the 3.x Go runtime, ChatGPT/Codex plugin package, npm package, and MCP
Registry entry.

## Candidate gate

- [ ] Worktree is clean and the intended commit is pushed to `main`.
- [ ] `go test ./...`, `go vet ./...`, and `git diff --check` pass with Go 1.26+.
- [ ] `scripts/validate-contracts.ps1` passes.
- [ ] `npm ci`, `npm test`, `npm run verify`, and `npm pack --dry-run` pass.
- [ ] Official `mcp-publisher validate server.json` passes.
- [ ] `scripts/smoke-e2e.ps1` passes on Windows, macOS, and Linux.
- [ ] `scripts/build-agent.ps1` or `scripts/build-agent.sh` creates all five target binaries.
- [ ] `scripts/verify-release.ps1 -Path <dist> -Version <version>` verifies checksums and the manifest.
- [ ] The packaged plugin starts the native `code-relay-agent mcp-stdio` binary.

## GitHub release

1. Confirm the version in `cmd/code-relay-agent/main.go` and
   `.codex-plugin/plugin.json`, `package.json`, and `server.json`.
2. Create and push an annotated tag, for example `v3.1.0`.
3. Confirm the release workflow publishes five binaries, `release.json`,
   `sbom.cdx.json`, and `SHA256SUMS`.
4. Download one artifact on each supported operating system and run
   `code-relay-agent version` and `code-relay-agent doctor --root <project>`.
5. Retain the workflow run URL and source commit in the release notes.

## npm and MCP Registry

1. Confirm the GitHub Release exists before npm publication; the npm launcher
   downloads assets from the matching `v<package-version>` release.
2. For the first `code-relay-mcp` publication, create the npm package with a
   short-lived `NPM_TOKEN` repository secret or an interactive maintainer
   publish. Remove the bootstrap token after use.
3. Configure npm trusted publishing for `zarcherlot/code-relay` and workflow
   `publish-mcp.yml`; subsequent publishes use GitHub OIDC.
4. Confirm `code-relay-mcp@<version>` is public before publishing
   `io.github.zarcherlot/code-relay` with `mcp-publisher`.
5. Verify the exact version through the npm registry and
   `GET /v0.1/servers/io.github.zarcherlot%2Fcode-relay/versions/<version>`.

## Rollback

- Pause the checkpoint workflow or disable the affected runner label.
- Restore the previous known-good binary and keep the project binding intact.
- Do not delete runbooks or receipts; re-run only after the source commit and
  receipt state have been reviewed.
- Record the failed release tag, workflow run, and recovery commit.
