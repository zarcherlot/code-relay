# Redis session and event contract

Redis is the shared, ephemeral coordination plane for a multi-instance
gateway. PostgreSQL remains the durable tenant and audit store; GitHub remains
the runbook/receipt source of truth.

The configured `CODE_RELAY_REDIS_KEY_PREFIX` is prepended to every key.

| Key | Type | TTL | Purpose |
| --- | --- | --- | --- |
| `session:<opaque-token>` | String (encrypted JSON) | session TTL | OAuth session subject, provider token, tenant/project binding, expiry and revocation flag |
| `oauth-code:<opaque-code>` | String (encrypted JSON) | code TTL | One-time MCP authorization code; consumed atomically with `GETDEL` |
| `mcp-session:<session-id>` | String (subject) | session TTL | Cross-instance MCP session ownership and expiry |
| `session-events:<session-id>` | Stream | event retention | Ordered SSE events; Redis stream IDs are exposed as `Last-Event-ID` cursors |
| `session-subs:<session-id>` | Set | session TTL | Active gateway instance subscription leases |
| `rate:<tenant>:<minute>` | String counter | 2 minutes | Distributed request rate limit |
| `lock:run:<run-id>` | String value | 2 minutes | Idempotent publish/dispatch lock; release only with owner token |

Session tokens are generated randomly and are never used as Redis key names
without the prefix. The Go adapter encrypts the session JSON with the
`CODE_RELAY_SESSION_SECRET`-derived key; deployments must still enable Redis
TLS and disk encryption. Do not put provider tokens in logs or SSE payloads.

`XADD` assigns the event cursor, `XREAD BLOCK` fans events to connected
instances, and bounded `XTRIM MAXLEN` plus stream TTL enforce retention. A
reconnecting client sends
`Last-Event-ID`; the gateway starts with the next Redis stream ID. If the
cursor has been trimmed, the gateway returns a resynchronization error and the
client must reinitialize.

All key writes must include tenant/session ownership checks in the gateway.
Pub/Sub is not a durability mechanism and must not replace the event stream.
