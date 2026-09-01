# Code Relay plugin publishing

This document records the current path from a source checkout to a local,
workspace, or public ChatGPT/Codex plugin listing.

## Build a desktop package

Run the packaging script on the target OS and architecture:

```powershell
pwsh -NoProfile -File ./scripts/package-plugin.ps1 -Output ./dist/plugin
```

The package contains `.codex-plugin/plugin.json`, the platform-native
`code-relay-agent`, `.mcp.json`, skills, schemas, templates, and brand assets.
The source checkout uses `go run` for development; the packaged plugin does not
need Go at runtime.

## Local/repository installation

For the complete operator procedure, see
[the local desktop install runbook](deploy/LOCAL-INSTALL-RUNBOOK.md).

The repository includes `.agents/plugins/marketplace.json` with the package path
already wired. A first-time local install is therefore just:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass `
  -File ./scripts/package-plugin.ps1 `
  -Output ./dist/plugin
```

Then restart the ChatGPT desktop app, open Plugins Directory, select
`Code Relay (Local)` → `Code Relay`, and click Install. This is a private
authoring/testing source and is not a public directory submission. If you receive
an already-built `dist/plugin`, skip the build command; Go is only needed for
source rebuilds.

Keep the default `dist/plugin` output so the checked-in marketplace entry remains
valid. If you choose another output directory, update its `source.path` accordingly.

## Workspace publication

A workspace administrator can open the plugin from the Personal source and
select Publish, then assign workspace roles. This makes the plugin available
only inside that organization.

## npm distribution

The public MCP package is `code-relay-mcp`. Its version must match the base
version in `.codex-plugin/plugin.json`, `server.json`, and the Go binaries.

```sh
npm ci
npm test
npm run verify
npm pack --dry-run
```

The package is a zero-dependency launcher. On first MCP startup it downloads
the matching platform binary from
`https://github.com/zarcherlot/code-relay/releases/download/v<version>/`,
checks the asset against that release's `SHA256SUMS`, and caches it per version.
Therefore the GitHub Release must exist before the npm version is published.

The first npm publication must be bootstrapped with an authenticated maintainer
or a short-lived `NPM_TOKEN` GitHub secret. After the package exists, configure
npm trusted publishing for repository `zarcherlot/code-relay`, workflow
`publish-mcp.yml`, and the `npm publish` action. Remove the bootstrap token;
later releases use GitHub OIDC and automatically receive npm provenance.

## MCP Registry publication

`server.json` publishes the npm package as
`io.github.zarcherlot/code-relay`. The npm package's `mcpName` must be exactly
the same value. Validate before release with the official publisher:

```sh
mcp-publisher validate server.json
```

`.github/workflows/publish-mcp.yml` verifies the npm version is public, logs in
to the Registry through GitHub OIDC, and publishes `server.json`. The release
workflow dispatches it after creating the matching GitHub Release. Publication
can be verified at:

```text
https://registry.modelcontextprotocol.io/v0.1/servers/io.github.zarcherlot%2Fcode-relay/versions/<version>
```

The MCP Registry is a metadata directory for MCP clients. It is independent of
the ChatGPT/Codex public plugin review described below.

## Public submission status

The repository now contains both the local stdio MCP package and a hosted
HTTPS MCP gateway. Hosted mode is enabled by the GitHub OAuth/GitHub App
environment variables documented in `deploy/remote-mcp.env.example`; it does
not mount a project checkout and uses GitHub as the runbook/receipt source of
truth. The deployment and rollout gates are documented in
`deploy/REMOTE-MCP-PLAN.md` and the audit cases in
`deploy/REMOTE-MCP-TEST-MATRIX.md`.

Public submission still requires deploying that gateway on a real HTTPS
domain, configuring the OAuth callback and GitHub App, completing domain
verification, and supplying reviewer-ready staging credentials when needed.

After that service is available:

1. Open <https://platform.openai.com/plugins>.
2. Create a `With MCP` plugin submission.
3. Complete the listing, verified developer identity, legal URLs, starter
   prompts, five positive tests, three negative tests, regions, and release
   notes.
4. Scan the MCP server, fix validation findings, and submit for review.

Approval publishes the plugin to the universal directory shared by ChatGPT and
Codex. A GitHub repository or release alone does not create a public listing.
