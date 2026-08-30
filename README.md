# Code Relay

<div align="center">
  <img src="assets/icon.png" alt="Code Relay Logo" width="96">
  <br>
  <sub>build, verify, and relay</sub>
</div>

[中文版](README.zh-CN.md)

![Code Relay workflow](assets/overview.png)

> **Code Relay is a cross-machine development and verification collaboration tool that lets Codex hand work between a development host and a target host.**

## Product overview

Relay is a cross-machine development and verification collaboration tool for Codex. AI on the development host implements the request; the verification environment on the target host runs real tests; the result returns as a structured receipt and can automatically drive the next repair iteration.

It is designed for teams that need verification across operating systems, private networks, GPUs, hardware, or production-like environments.

Relay is more than “send a command to another machine.” It connects **development, publishing, real-environment verification, result delivery, and failure-driven repair** into one traceable loop.

## Product logic

```text
User describes the request in Codex
        ↓
Dev Host: AI develops, commits, and merges the code
        ↓
Relay: carries the exact commit and task
        ↓
Target Host: runs the validation in the real target environment
        ↓
Receipt: returns structured results and evidence
        ↓
Pass → done          Fail → repair and retry          Blocked → ask for a decision
```

A typical task can start with one sentence:

> Implement idempotency for the payment callback, merge it, and have the target host verify it; if verification fails, keep repairing until it passes.

Codex handles development and orchestration, Relay carries the task between hosts, and the target host performs the verification. Users see meaningful stages such as “verifying,” “verification failed,” and “done,” rather than a wall of GitHub Actions details.

## Who thinks of using Relay?

Relay is for teams that often find themselves saying, “It works here, but we need another machine to prove it”:

- **Heavy AI-coding users**: the laptop has no GPU, so a GPU server should verify the model code automatically instead of requiring manual packaging and SSH.
- **Backend developers**: a payment callback cannot be reproduced locally and must run against the real database, queue, and private-network dependencies.
- **DevOps and QA engineers**: every merge currently requires a manual request for regression testing on another machine, making tests easy to miss and hard to audit.
- **GPU / AI engineers**: the code passes on CPU but must be verified on a machine with the correct CUDA stack.
- **Hardware, IoT, and robotics teams**: only the machine connected to the real device can prove that the serial port, camera, or sensor actually works.
- **Private-network and enterprise teams**: the development host cannot access the production-like services, so an isolated target host must perform the verification.
- **Browser-automation teams**: local browser versions differ from production, so the user journey must run in a specified environment.

## What the MVP provides

- **Branch-scoped binding**: identify the project with `repository + refs/heads/<branch>`.
- **Natural-language setup**: ask Codex to enable Relay and generate a target-host join link.
- **Short-lived invitation links**: `code-relay://join/...` includes a repository and branch preview.
- **Safe target-host bootstrap**: an unbound workspace can clone the approved repository; non-empty or unrelated directories are never overwritten.
- **GitHub Actions verification**: route tasks to a `codex-b` self-hosted runner; the Go agent verifies the exact source commit in an isolated worktree.
- **Reliable receipts**: idempotent task publication, source-commit binding, receipt validation, command allowlisting, timeouts, and failure/blocked receipts.
- **GitHub Actions support**: run target-environment verification on a `codex-b` self-hosted runner.

## Install from Codex

Install **Code Relay** from the Codex plugin UI. The plugin starts the bundled Go `code-relay-agent`; no language runtime installation is required.

### Dev Host: bind the current project

Open the project in Codex and say:

> Enable Code Relay for the current project and branch, then generate a Target Host join link.

After showing the resolved remote repository and branch, the plugin writes `.code-relay/project.json`, adds the project integration assets, pushes the binding, and returns a short-lived invitation link.

### Target Host: join and verify

Install the same plugin on the Target Host and paste the invitation link into Codex. After confirmation:

1. An empty or unassociated workspace is cloned to the invited repository and branch.
2. A verifier binding is written and the target host is registered as a GitHub Actions runner with the `codex-b` label.
3. New tasks are fetched from the bound remote ref and placed in the private inbox.
4. GitHub Actions invokes `code-relay-agent run-pending`, which executes the declared validation plan and publishes a receipt.

If the current directory is non-empty or belongs to another project, Code Relay leaves it untouched and asks for a separate target directory.

The complete user journey is documented in [USER_GUIDE.md](USER_GUIDE.md).

## Repository layout

```text
.codex-plugin/plugin.json       # Codex plugin manifest
.mcp.json                       # plugin-local MCP server
skills/                         # orchestrator and verifier Skills
cmd/code-relay-agent/           # Go CLI and MCP entrypoint
internal/relay/                 # Go protocol, runner, Git and MCP implementation
schemas/                        # task and receipt JSON Schemas
templates/                      # task and receipt Markdown templates
.github/workflows/              # optional Target Host self-hosted workflow
tests/                          # local MVP and end-to-end tests
```

## Developer validation

The repository is dependency-light and requires Go 1.22+:

```powershell
go test -race ./...
go vet ./...
go build ./cmd/code-relay-agent
```

The CLI is a developer/CI fallback, not the normal installation path:

```powershell
code-relay-agent publish --root . --file examples/task-001.md --no-git
code-relay-agent run-task task-001 --root .
code-relay-agent status --root .
```

The Go runtime ships for Linux, macOS, and Windows (amd64 and arm64 where applicable):

```powershell
./scripts/build-agent.ps1
```

To build a plugin bundle with the platform-specific MCP executable and `.mcp.json`, run:

```powershell
./scripts/package-plugin.ps1
```

For a versioned release, run `./scripts/release.ps1` on Windows/PowerShell or `./scripts/build-agent.sh` on macOS/Linux; the release workflow produces five agent binaries, `release.json`, SBOM metadata, and `SHA256SUMS`.

On a Target Host, install the self-hosted GitHub Actions runner with the `codex-b` label and place `code-relay-agent` in the repository's `bin/` directory. The workflow invokes `run-pending`; no Relay watcher or Python runtime is required.

Run `code-relay-agent doctor --root .` to diagnose a host without changing project state.

## Security and operating boundaries

Code Relay is an MVP. Validation commands run without a shell, are allowlisted, have bounded output and timeouts, and destructive command patterns are blocked. Users must still review task content and runner permissions.

Invitations are short-lived bearer links by default. Controlled deployments can set the same 32+ character `CODE_RELAY_INVITE_SECRET` on the Dev Host and Target Host to enable HMAC integrity checks. Per-user authorization and revocation still require a hosted control plane.

Read [SECURITY.md](SECURITY.md) before exposing a runner or webhook, [CONTRIBUTING.md](CONTRIBUTING.md) before submitting changes, and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) before participating.

## Project status

The current release is an MVP reference implementation. The protocol and directory layout are intentionally small so that a team can inspect, reuse, or replace individual runtime components.

## License

Code Relay is released under the [MIT License](LICENSE).
