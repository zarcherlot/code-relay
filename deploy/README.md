# Code Relay runtime deployment

## Linux

Copy the matching `code-relay-agent-linux-*` binary to `/usr/local/bin/code-relay-agent`, create a dedicated `code-relay` user, and install `code-relay-agent.service` under `/etc/systemd/system/`. Set the project path and run:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now code-relay-agent
```

## Windows

Run `deploy/install-code-relay-service.ps1` from an elevated PowerShell session to register `code-relay-agent-windows-amd64.exe` as an automatic service. Use a dedicated service account and grant it access only to the verifier project directory. The agent itself handles Ctrl+C shutdown and bounded child processes.
