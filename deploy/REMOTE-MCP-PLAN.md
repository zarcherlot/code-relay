# Remote MCP deployment plan for Code Relay

## Decision

Keep the existing Go agent and GitHub Actions verifier as the execution/data
plane. Add a small, separately deployed Go MCP gateway as the public control
plane. The gateway exposes the reviewed MCP tools over Streamable HTTP at
`/mcp`; it must never expose a user's local project directory or bind the
existing localhost daemon directly to the Internet.

## Request flow

```text
ChatGPT plugin
  -> HTTPS /mcp + OAuth 2.1
  -> Code Relay MCP gateway
  -> GitHub API (task commit / workflow dispatch / receipt read)
  -> dedicated codex-b self-hosted runner
  -> existing code-relay-agent run-pending
  -> receipts/<task-id> pushed to the bound branch
```

The gateway is stateless for task execution. GitHub remains the source of
truth for repositories, task commits, workflow runs, and receipts. A small
database is only needed for OAuth state, installation-to-repository bindings,
rate limits, and audit references.

## Recommended first deployment

1. Build a new `cmd/code-relay-mcp` HTTP adapter around the existing MCP tool
   handlers. Keep `mcp-stdio` unchanged for desktop/local use.
2. Deploy the gateway as a single Go container on Cloud Run, Fly.io, or an
   equivalent HTTPS container platform. Use a custom host such as
   `mcp.code-relay.example` and expose `/mcp`.
3. Use OAuth 2.1 with GitHub as the identity provider. Store only encrypted
   installation references or short-lived tokens; never accept GitHub tokens in
   prompt text or tool arguments.
4. Use a GitHub App with the minimum repository permissions needed to create
   task commits, dispatch the verifier workflow, and read receipts. Keep the
   existing `codex-b` runner isolated and protected by repository/branch
   policy.
5. Add request size, concurrency, timeout, and per-user rate limits at the
   gateway. Return structured MCP errors and redact tokens, remotes, and
   runner details from responses and logs.
6. Add `/.well-known/openai-apps-challenge` on the gateway host for OpenAI
   domain verification, then scan the endpoint with the plugin submission
   portal.

## Tool boundary

The public gateway should initially expose only the existing safe workflow:

- bind/status/doctor for an authorized repository;
- publish a task through the existing task schema;
- fetch/analyze a receipt;
- validate task content before dispatch.

It should not expose arbitrary shell execution, local filesystem paths, runner
registration, invitation secrets, or service-management commands. Those remain
local/admin operations.

## Rollout gates

1. Unit and race tests for the HTTP adapter and authentication middleware.
2. MCP Inspector checks for tool schemas, annotations, errors, and limits.
3. A staging GitHub App and isolated `codex-b` runner.
4. Positive and negative ChatGPT evaluation prompts.
5. Privacy policy update covering hosted data retention and subprocessors.
6. Only then submit the public plugin; keep the local stdio package available
   for desktop-only/private installations.

## Main risks

- A remote gateway cannot safely infer a local project root; repository and
  branch must be explicit and authorized.
- GitHub App permissions and runner isolation are more important than the
  transport choice.
- Public MCP metadata is a reviewed contract. Tool names, schemas, annotations,
  and response fields must remain stable between submissions.
