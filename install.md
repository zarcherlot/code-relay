# Install Code Relay MCP

These instructions are written for an autonomous AI client. Read the entire
guide before changing configuration. Code Relay runs through npm and downloads
the matching versioned release artifact from GitHub; the launcher verifies the
artifact against the release `SHA256SUMS` before executing it.

## Safety contract

1. Explain that installation adds one local MCP server named `code-relay` and
   requires Node.js plus network access to npm and GitHub Releases.
2. Ask the user for confirmation before modifying an AI-client configuration.
3. Never overwrite the whole MCP configuration. The installer merges the
   `code-relay` entry, preserves other servers, and backs up an existing JSON
   file before writing it.
4. Do not use `--force` unless the user explicitly approves replacement of an
   existing `code-relay` entry.
5. Do not request or place GitHub, npm, or Code Relay secrets in command-line
   arguments or configuration files.

## Step 1: Check Node.js

Run:

```sh
node --version
npm --version
```

Node.js 18 or newer is required. If it is unavailable or too old, stop and ask
the user to install a current Node.js LTS release.

The commands below use `npx` so no persistent npm installation is required. If
the user explicitly prefers a global command, use `npm install --global
code-relay-mcp` and replace the leading `npx -y code-relay-mcp@latest` with
`code-relay-mcp` in later steps.

## Step 2: Identify the active AI client

Choose exactly one supported client identifier:

- `codex` for Codex CLI, desktop, or IDE clients using the Codex MCP config.
- `claude-code` for a project-scoped `.mcp.json`.
- `cursor` for a project-scoped `.cursor/mcp.json`.
- `vscode` for a project-scoped `.vscode/mcp.json`.
- `generic` when the client uses another MCP configuration format.

For project-scoped clients, confirm the project root before continuing.

## Step 3: Preview the installation

Run the matching command without `--yes` first:

```sh
npx -y code-relay-mcp@latest install --client codex
npx -y code-relay-mcp@latest install --client claude-code --root /absolute/project/path
npx -y code-relay-mcp@latest install --client cursor --root /absolute/project/path
npx -y code-relay-mcp@latest install --client vscode --root /absolute/project/path
npx -y code-relay-mcp@latest install --client generic
```

Show the resulting preview to the user. For `generic`, merge the printed
`mcpServers.code-relay` entry using the client's own supported configuration
mechanism; do not guess an undocumented path.

## Step 4: Apply after confirmation

Repeat the selected command with `--yes`. Example:

```sh
npx -y code-relay-mcp@latest install --client codex --yes
```

The installer pins the resolved package version in the saved client
configuration. A normal MCP launch then uses:

```json
{
  "command": "npx",
  "args": ["-y", "code-relay-mcp@3.1.0"]
}
```

## Step 5: Verify

Run:

```sh
npx -y code-relay-mcp@latest version
```

Then restart or reload the AI client if it does not refresh MCP servers
dynamically. Confirm that a server named `code-relay` is listed and that its
tools include `bind_project`, `publish_runbook`, and `fetch_receipt`.

If installation fails, report the command, client identifier, Node/npm
versions, operating system, and exact error. Do not print access tokens or
environment-variable values.
