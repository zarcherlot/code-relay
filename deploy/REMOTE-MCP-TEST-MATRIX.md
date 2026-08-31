# Hosted MCP audit test matrix

The repository's automated tests cover the security and protocol gates below.
Run `go test ./...` and `go test -race ./...` before connecting the staging
GitHub App. The final three rows require a staging GitHub App and isolated
`codex-b` runner because they exercise real GitHub Actions behavior.

| Class | Scenario | Expected result | Automated coverage |
| --- | --- | --- | --- |
| Positive | OAuth authorization-code callback with PKCE | Session cookie is issued; OAuth token is encrypted and never returned | `TestOAuthPKCEAndEncryptedSession` |
| Positive | User-authorized repository and branch binding | GitHub App installation token is minted and binding is returned | `TestGitHubAppRepositoryAPI`, remote backend tests |
| Positive | Publish task through Contents API | `tasks/<id>/task.md` is committed and verifier workflow is dispatched | `TestGitHubRemotePublishAndDispatch` |
| Positive | Fetch/analyze receipt from GitHub Contents API | Receipt is validated against the task and analyzed | `GitHubRemoteBackend.fetchReceipt` path |
| Negative | Missing or mismatched OAuth state | HTTP 400; no session is created | `TestOAuthRejectsStateReplayOrMismatch` |
| Negative | Missing session on `/mcp` | HTTP 401 with a Bearer challenge | `TestRemoteHTTPRequiresOAuthAndDoesNotAcceptRoot` |
| Negative | Repository outside the user's App installation | Request rejected before Contents/Actions calls | `UserCanAccessRepository` checks |
| Negative | Arbitrary filesystem `root` in hosted tool arguments | Argument is removed; only repository/ref are accepted | `TestRemoteHTTPRequiresOAuthAndDoesNotAcceptRoot` |
| Boundary | Oversized JSON, invalid content type, rate/concurrency limits | Structured rejection without invoking a tool | `mcp_http_test.go` |
| Boundary | Invalid repository, branch, workflow, task ID, or task size | Validation error; no GitHub mutation | normalization and contract tests |
| Staging | Positive publish → workflow → receipt | Receipt is pushed to the bound branch and fetch/analyze returns `done` | Run with `CODE_RELAY_GITHUB_*` staging credentials |
| Staging | Negative unauthorized repository/branch and malformed task | No commit/dispatch; MCP error is redacted | Run with staging App and test repository |
| Staging | Boundary retry/duplicate publish and concurrent calls | Idempotent conflict handling and rate limits remain bounded | Run with staging App and MCP Inspector |

Hosted audit logs are JSON and contain subject/login, tool, outcome, and
duration only. Access tokens, private keys, repository credentials, and runner
details must not be copied into prompts, receipts, or logs.

