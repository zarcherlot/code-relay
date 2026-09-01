# Code Relay checkpoint runbook

The supported production path is GitHub Actions with a dedicated self-hosted
runner labelled `code-relay-checkpoint`. The local daemon service definitions are optional
for controlled environments and are not started by the plugin.

`code-relay-checkpoint` replaces the legacy `codex-b` label. Existing runners
must be relabelled (or a new runner registered) before using the updated
workflow; GitHub Actions will not schedule a job onto a runner that only has
the old label.

## Bootstrap

1. Use a dedicated low-privilege account and an empty checkout of the bound
   repository and branch.
2. Install the matching release binary for the host OS and verify its SHA-256
   against `SHA256SUMS`.
3. Run `code-relay-agent doctor --root <project>` before registering the runner.
4. Register the runner with the exact `code-relay-checkpoint` label and prevent overlapping
   jobs on the same checkpoint host.
5. Configure `CODE_RELAY_INVITE_SECRET` when signed invitations are required.

## Healthy execution

- A runbook commit appears below `runbooks/<runbook-id>/runbook.md`.
- The workflow checks out the runbook commit and runs `run-pending`.
- A receipt is written below `receipts/<runbook-id>/` and pushed to the bound ref.
- `fetch-receipt` and `analyze` report the same runbook ID and source commit.

## Troubleshooting

| Symptom | Checks |
|---|---|
| Workflow is queued | Runner is online and has the `code-relay-checkpoint` label; no other job owns the concurrency group. |
| Worktree is blocked | Source commit exists on the bound ref; remove only stale `.code-relay/worktrees/<runbook-id>` after reviewing Git state. |
| Receipt is not pushed | Check token contents permission, branch/ref binding, and the workflow log; do not force-push. |
| Doctor reports an error | Fix the named root, Git, metadata, binding, or remote check and run doctor again. |
| Repeated failures | Pause the runner, preserve runbooks/receipts, collect the run URL and agent version, then roll back using the release checklist. |

Never copy invitation URLs, runner tokens, or credential-bearing remotes into
logs or issue reports.
