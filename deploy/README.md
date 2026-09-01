# Code Relay runtime deployment

The supported default checkpoint path is the repository's GitHub Actions workflow on a dedicated `code-relay-checkpoint` self-hosted runner. The service definitions below are optional compatibility examples for controlled deployments that deliberately choose the local `daemon` command; the plugin does not install or start them automatically.

The daemon binds to `127.0.0.1` by default. Binding to any other address is
rejected unless `CODE_RELAY_WEBHOOK_SECRET` is configured with at least 32
characters; keep the listener behind a firewall even when authenticated.

## Linux

Copy the matching `code-relay-agent-linux-*` binary to `/usr/local/bin/code-relay-agent`, create a dedicated `code-relay` user, and install `code-relay-agent.service` under `/etc/systemd/system/`. Set the project path and run:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now code-relay-agent
```

## Windows

Run `deploy/install-code-relay-service.ps1` from an elevated PowerShell session to register `code-relay-agent-windows-amd64.exe` as an automatic service. Use a dedicated service account and grant it access only to the checkpoint project directory. The agent itself handles Ctrl+C shutdown and bounded child processes.

## macOS

Use the matching `code-relay-agent-darwin-arm64` (Apple Silicon) or `code-relay-agent-darwin-amd64` (Intel) binary. Install it as `/usr/local/bin/code-relay-agent`, make it executable, and copy `com.code-relay.agent.plist` to `~/Library/LaunchAgents/` after replacing `REPLACE_WITH_PROJECT_ROOT` and `REPLACE_WITH_HOME` with the real paths. Then load it for the logged-in user:

```bash
chmod 0755 /usr/local/bin/code-relay-agent
launchctl bootstrap "gui/$(id -u)" "$HOME/Library/LaunchAgents/com.code-relay.agent.plist"
launchctl enable "gui/$(id -u)/com.code-relay.agent"
```

The plist runs the checkpoint daemon with launchd-managed restart and logs. For a system-wide daemon, use a root-owned LaunchDaemon plist and a dedicated service account instead of LaunchAgents.
