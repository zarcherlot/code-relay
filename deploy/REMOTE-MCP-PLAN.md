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

## Implementation status

The hosted path is now implemented in the Go gateway. When the GitHub OAuth,
GitHub App, and session-secret variables are present, `cmd/code-relay-mcp`
starts in hosted mode; otherwise it keeps the local Bearer-token staging mode.
Hosted task and receipt operations use the GitHub Contents and Actions APIs and
never read or write `CODE_RELAY_MCP_ROOT`.

Authentication endpoints are `/auth/github` (OAuth start),
`/auth/github/callback` (PKCE callback), `/auth/github/install` (start App
installation), `/auth/github/app-callback` (bind installation to the signed-in
user), and `/auth/logout`. MCP clients connect to `/mcp` with the encrypted
session cookie established by that flow.

OAuth state and sessions are encrypted, short-lived cookies, so the gateway
does not require a local database or persistent container volume for identity
state. GitHub remains the source of truth for repository authorization,
branches, task commits, workflow dispatches, and receipts. Rate-limit counters
and audit events are process-local today; deploy one gateway instance or put a
shared edge rate limiter in front of a multi-instance deployment.
Set `CODE_RELAY_ALLOWED_REFS` when the hosted deployment should limit users to
an explicit branch allowlist (for example `owner/repo@refs/heads/main`).

## Recommended deployment

1. Build `cmd/code-relay-mcp`. Keep `mcp-stdio` unchanged for desktop/local
   use. Hosted mode exposes only the repository-scoped tools over `/mcp`, plus
   `/healthz` and the GitHub OAuth/App installation endpoints.
2. Deploy the gateway as a single Go container on Cloud Run, Fly.io, or an
   equivalent HTTPS container platform. Use a custom host such as
   `mcp.code-relay.example` and expose `/mcp`.
3. Configure the GitHub OAuth application and the GitHub App using
   `deploy/remote-mcp.env.example`. The gateway uses authorization code + PKCE,
   encrypts the session cookie, verifies the user's installation membership,
   and mints a short-lived installation token for each API operation.
4. Grant the GitHub App only Contents (read/write) and Actions (read/write)
   permissions needed for task commits, workflow dispatch, and receipt reads.
   Keep the existing `codex-b` runner isolated and protected by
   repository/branch policy.
5. Add request size, concurrency, timeout, and per-user rate limits at the
   gateway. Return structured MCP errors and redact tokens, remotes, and
   runner details from responses and logs.
6. Add `/.well-known/openai-apps-challenge` on the gateway host for OpenAI
   domain verification, then scan the endpoint with the plugin submission
   portal.

## Local/container smoke test

Build and run the staging gateway with Docker:

```bash
docker build -t code-relay-mcp .
docker run --rm -p 8080:8080 \
  -e CODE_RELAY_MCP_ROOT=/workspace/project \
  -e CODE_RELAY_MCP_TOKEN="replace-with-a-random-32-character-secret" \
  -v "$PWD:/workspace/project:ro" \
  code-relay-mcp
```

The checkout should be writable for tools such as `publish_task`; use a
dedicated staging checkout and least-privilege credentials rather than the
read-only mount shown above when testing write workflows.

The Bearer-token/fixed-root path remains available only as a local staging
fallback. Hosted mode is the GitHub App-backed repository adapter and does not
require `CODE_RELAY_MCP_ROOT`. Set `OPENAI_APPS_CHALLENGE` only when the
submission portal supplies a domain-verification token; the gateway then serves
that exact token at `/.well-known/openai-apps-challenge`.

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

1. Unit and race tests for OAuth/PKCE, GitHub API calls, the HTTP adapter,
   authentication middleware, and remote tool routing.
2. MCP Inspector checks for tool schemas, annotations, errors, and limits.
3. A staging GitHub App and isolated `codex-b` runner.
4. Positive, negative, and boundary cases from
   `deploy/REMOTE-MCP-TEST-MATRIX.md`.
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
