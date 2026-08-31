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

## Public submission status

The repository now contains both the local stdio MCP package and a hosted
HTTPS MCP gateway. Hosted mode is enabled by the GitHub OAuth/GitHub App
environment variables documented in `deploy/remote-mcp.env.example`; it does
not mount a project checkout and uses GitHub as the task/receipt source of
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
