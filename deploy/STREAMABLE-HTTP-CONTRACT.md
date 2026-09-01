# Code Relay hosted transport contract

Code Relay has two supported transports:

| Deployment | Transport | Endpoint |
| --- | --- | --- |
| Local/desktop | MCP `stdio` | process stdin/stdout |
| Hosted/SaaS | MCP Streamable HTTP | `https://<host>/mcp` |

The hosted gateway exposes one MCP endpoint. It is not a proxy for a local
checkout: repository, ref, tenant, and project authorization are resolved by
the gateway before a GitHub API operation is attempted.

## HTTP contract

The endpoint supports `POST`, `GET`, and `DELETE`:

- `POST /mcp` accepts one JSON-RPC message. The client must send
  `Content-Type: application/json` and advertise
  `Accept: application/json, text/event-stream`.
- A short request may return `application/json`. A request that emits progress
  or other server messages returns `text/event-stream`; the stream always ends
  with the JSON-RPC response for that POST.
- `GET /mcp` opens the session event stream. The client sends
  `Accept: text/event-stream` and the negotiated `Mcp-Session-Id`.
- `DELETE /mcp` explicitly closes the session identified by
  `Mcp-Session-Id`.

After `initialize`, the server may issue an opaque `Mcp-Session-Id` response
header. Subsequent requests for that session must send the same header. The
server negotiates `MCP-Protocol-Version` during initialization and rejects an
unsupported version with a structured `400` response.

Events use normal SSE framing and carry JSON-RPC messages:

```text
id: 01J...
event: message
data: {"jsonrpc":"2.0","method":"notifications/progress","params":{}}
```

Each event ID is monotonic within a session. A reconnecting client may send
`Last-Event-ID`; the gateway replays events still inside the configured
retention window. If the requested ID is older than retained history, the
client must reinitialize to resynchronize. Disconnecting an event stream never
cancels a running checkpoint.

## Authentication and browser safety

Hosted requests use an OAuth 2.1 bearer access token. The gateway publishes
Protected Resource Metadata at:

```text
GET /.well-known/oauth-protected-resource
```

Unauthorized requests return `401` with a `WWW-Authenticate` challenge that
points to this metadata document. The metadata identifies the authorization
server, resource URL, and supported scopes. Browser-facing OAuth state uses an
opaque cookie; GitHub access tokens remain server-side and encrypted.

The gateway validates `Origin` on every browser-capable request, sets an
explicit allow-list for CORS, and never accepts a caller-provided local
filesystem root in hosted tool arguments.

## Operational requirements

The event stream is long-lived. The reverse proxy must disable buffering,
forward HTTP/1.1, preserve the `Host` and authorization headers, and allow an
idle timeout longer than the heartbeat interval. See
`deploy/reverse-proxy-streamable-http.conf.example` for an Nginx baseline.

For more than one gateway instance, PostgreSQL is the durable control-plane
store and Redis is the shared session/event/rate-limit store. Sticky sessions
are not a correctness mechanism. The gateway must drain event streams during
graceful shutdown and enforce per-tenant connection and queue limits.
