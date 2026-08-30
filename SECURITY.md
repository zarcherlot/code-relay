# Security Policy

## Scope

Codex Relay moves source references, task instructions, validation commands, and receipts through Git. It can also start a local watcher and execute commands on a verifier host. Treat every task and invitation as untrusted input until it has been reviewed.

## Current MVP boundaries

- Invitations are short-lived bearer links by default. Set `CODEX_RELAY_INVITE_SECRET` to the same 32+ character secret on A and B to add HMAC integrity verification; anyone who obtains an unexpired unsigned link can otherwise attempt to join the encoded repository/ref. Do not paste invitations or secrets into public issues or logs.
- The verifier runtime allowlists common development executables and blocks several destructive command patterns, but this is defense-in-depth rather than a sandbox.
- Use a dedicated low-privilege self-hosted runner and a separate worktree for validation.
- Give GitHub tokens only the minimum contents/actions permissions required to fetch tasks and publish receipts.
- Keep webhook secrets out of Git and validate `X-Hub-Signature-256` when a webhook is enabled.

## Reporting a vulnerability

Please do not open a public issue for an exploitable security problem. Contact the maintainers privately with:

- a short impact summary;
- affected version or commit;
- reliable reproduction steps or a minimal proof of concept;
- any suggested mitigation.

Until a private security contact is configured for the project, use the repository owner’s private security-advisory channel. Do not include credentials or real customer data in the report.

## Future hardening

Before production use, the project should add asymmetric signed invitations or a hosted authorization service, invitation revocation, per-project authorization, stronger process isolation, and an auditable event store. The optional HMAC mode is an integrity layer for controlled deployments, not a replacement for per-user authorization.
