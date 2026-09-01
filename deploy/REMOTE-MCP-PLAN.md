# Remote MCP deployment plan for Code Relay

## Decision

Keep the existing Go agent and GitHub Actions checkpoint as the execution/data
plane. Add a small, separately deployed Go MCP gateway as the public control
plane. The gateway exposes the reviewed MCP tools over Streamable HTTP at
`/mcp`; it must never expose a user's local project directory or bind the
existing localhost daemon directly to the Internet.

## Request flow

```text
ChatGPT plugin
  -> HTTPS /mcp + OAuth 2.1
  -> Code Relay MCP gateway
  -> GitHub API (runbook commit / workflow dispatch / receipt read)
  -> dedicated code-relay-checkpoint self-hosted runner
  -> existing code-relay-agent run-pending
  -> receipts/<runbook-id> pushed to the bound branch
```

The gateway is stateless for runbook execution. GitHub remains the source of
truth for repositories, runbook commits, workflow runs, and receipts. A small
database is only needed for OAuth state, installation-to-repository bindings,
rate limits, and audit references.

## Implementation status

The hosted path is being upgraded to the Streamable HTTP contract in
`deploy/STREAMABLE-HTTP-CONTRACT.md`. Local/desktop use remains MCP `stdio`.
When the GitHub OAuth, GitHub App, and session-secret variables are present,
`cmd/code-relay-mcp` starts in hosted mode. Hosted runbook and receipt
operations use the GitHub Contents and Actions APIs and never read or write
`CODE_RELAY_MCP_ROOT`.

This branch now includes a standards-compliant single-client authorization-code
and PKCE token exchange. It is still not a public SaaS launch: the reference
process-local session/code store must be replaced by Redis (and the durable
tenant/audit layer by PostgreSQL), and client registration plus multi-instance
key/session rotation must be completed before public ChatGPT submission.

Authentication endpoints are `/auth/github` (OAuth start),
`/auth/github/callback` (PKCE callback), `/auth/github/install` (start App
installation), `/auth/github/app-callback` (bind installation to the signed-in
user), and `/auth/logout`. MCP clients connect to `/mcp` with the opaque
server-side session cookie established by that flow. The encrypted-cookie
mode remains available for compatibility testing only.

The hosted binding flow accepts repository/ref from the active ChatGPT project
context. `bind_project` can omit those fields after OAuth; the gateway resolves
them from the session binding. OAuth attempts to locate a selected-repository
installation for the configured App automatically and rejects installations
configured for all repositories. If no suitable installation exists, the user
is sent through the App installation flow for the requested repository; the
installation ID is never a user-facing input.

OAuth state is sealed and short-lived; browser sessions use opaque server-side
cookies when `CODE_RELAY_SESSION_STORE=memory` (or a future Redis adapter).
For SaaS production, PostgreSQL is required for durable tenants, members, project
bindings, runs, and audit references; Redis is required for shared MCP session
state, event replay, rate limits, and distributed locks. GitHub remains the
source of truth for repository authorization, branches, runbook commits,
workflow dispatches, and receipts. Do not use process-local state when more
than one gateway instance is deployed.
The initial PostgreSQL tables are defined in `deploy/migrations/001_control_plane.sql`;
Redis key/stream semantics are specified in `deploy/REDIS-CONTRACT.md`.
`deploy/docker-compose.saas.yml` provides a local PostgreSQL/Redis/gateway
integration environment; do not use its development credentials in production.
Set `CODE_RELAY_ALLOWED_REFS` when the hosted deployment should limit users to
an explicit branch allowlist (for example `owner/repo@refs/heads/main`).

## Recommended deployment

1. Build `cmd/code-relay-mcp`. Keep MCP `stdio` unchanged for desktop/local
   use. Hosted mode exposes the repository-scoped tools over Streamable HTTP at
   `/mcp`, plus `/healthz`, OAuth metadata, and GitHub OAuth/App installation
   endpoints.
2. Deploy the gateway as a single Go container on Cloud Run, Fly.io, or an
   equivalent HTTPS container platform. Use a custom host such as
   `mcp.code-relay.example` and expose `/mcp`.
3. Configure the GitHub OAuth application, GitHub App, PostgreSQL, and Redis
   using `deploy/remote-mcp.env.example`. The gateway uses authorization code +
   PKCE, publishes Protected Resource Metadata, encrypts server-side token
   state, verifies the user's installation membership, and mints a short-lived
   installation token for each API operation.
4. Grant the GitHub App only Contents (read/write) and Actions (read/write)
   permissions needed for runbook commits, workflow dispatch, and receipt reads.
   Keep the existing `code-relay-checkpoint` runner isolated and protected by
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

The checkout should be writable for tools such as `publish_runbook`; use a
dedicated staging checkout and least-privilege credentials rather than the
read-only mount shown above when testing write workflows.

The Bearer-token/fixed-root path remains available only as a local staging
fallback. Hosted mode is the GitHub App-backed repository adapter and does not
require `CODE_RELAY_MCP_ROOT`. Set `OPENAI_APPS_CHALLENGE` only when the
submission portal supplies a domain-verification token; the gateway then serves
that exact token at `/.well-known/openai-apps-challenge`.

For an ingress baseline, apply
`deploy/reverse-proxy-streamable-http.conf.example`. Buffering must be disabled
for SSE responses, HTTP/1.1 must be preserved, and the proxy read timeout must
exceed the gateway heartbeat interval. Use a graceful drain window during
deploys so active `/mcp` event streams can reconnect with `Last-Event-ID`.

## Tool boundary

The public gateway should initially expose only the existing safe workflow:

- bind/status/doctor for an authorized repository;
- publish a runbook through the existing runbook schema;
- fetch/analyze a receipt;
- validate runbook content before dispatch.

It should not expose arbitrary shell execution, local filesystem paths, runner
registration, invitation secrets, or service-management commands. Those remain
local/admin operations.

## Rollout gates

1. Unit and race tests for OAuth/PKCE, GitHub API calls, the HTTP adapter,
   authentication middleware, and remote tool routing.
2. MCP Inspector checks for tool schemas, annotations, errors, and limits.
3. A staging GitHub App and isolated `code-relay-checkpoint` runner.
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
