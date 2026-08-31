# Code Relay Privacy Notice

Last updated: 2026-08-31

Code Relay is an open-source plugin and local Go agent. In the current
distribution, the agent runs on the user's machine and does not send project
files, runbook contents, command output, or receipts to a Code Relay-operated
cloud service. The agent may access the repository, Git configuration, and
the GitHub account or runner credentials that the user explicitly configures.

When Code Relay is used with GitHub or a self-hosted runner, data is exchanged
with those services under their terms and privacy policies. Users should not
place tokens, invitation secrets, or other credentials in runbook text or logs.

The project does not use third-party analytics or advertising in the local
plugin. Security reports and support requests may be submitted through the
project's GitHub repository. This notice must be revised before enabling a
hosted Code Relay MCP service so that the hosted service's data retention,
authentication, and subprocessors are described accurately.

Contact and updates: <https://github.com/zarcherlot/code-relay/issues>
