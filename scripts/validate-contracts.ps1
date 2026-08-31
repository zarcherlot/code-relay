$ErrorActionPreference = "Stop"
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot "..")).Path

function Read-Json([string]$relativePath) {
  $path = Join-Path $root $relativePath
  if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
    throw "Missing contract file: $relativePath"
  }
  try {
    return Get-Content -LiteralPath $path -Raw | ConvertFrom-Json
  } catch {
    throw "Invalid JSON in ${relativePath}: $($_.Exception.Message)"
  }
}

$schemaFiles = @(
  "schemas/binding.schema.json",
  "schemas/receipt.schema.json",
  "schemas/task.schema.json"
)
foreach ($file in $schemaFiles) {
  $schema = Read-Json $file
  if (-not $schema.'$schema') {
    throw "Schema is missing `$schema: $file"
  }
}
$policy = Read-Json "schemas/runtime-policy.json"
foreach ($field in @("allowed_commands", "denied_command_arguments", "deny_tokens", "shell_operators", "sensitive_env_keys")) {
  if ($null -eq $policy.$field) { throw "Runtime policy is missing $field" }
}

$manifest = Read-Json ".codex-plugin/plugin.json"
if ($manifest.name -ne "code-relay" -or $manifest.version -ne "2.0.0") {
  throw "Plugin manifest must identify code-relay 2.0.0"
}
if ($manifest.interface.displayName -ne "Code Relay") {
  throw "Plugin display name must be Code Relay"
}

$mcp = Read-Json ".mcp.json"
$server = $mcp.'code-relay'
if (-not $server -or $server.command -ne "go" -or ($server.args -join " ") -ne "run ./cmd/code-relay-agent mcp-stdio") {
  throw "Source MCP configuration must launch the Go mcp-stdio entrypoint"
}

$appManifest = Read-Json "deploy/github-app-manifest.json"
foreach ($permission in @("contents", "actions", "metadata")) {
  if ($null -eq $appManifest.default_permissions.$permission) { throw "GitHub App manifest is missing $permission permission" }
}
$remoteEnv = Join-Path $root "deploy/remote-mcp.env.example"
if (-not (Test-Path -LiteralPath $remoteEnv -PathType Leaf)) { throw "Missing hosted MCP environment example" }
$remoteEnvText = Get-Content -LiteralPath $remoteEnv -Raw
foreach ($name in @("CODE_RELAY_GITHUB_OAUTH_CLIENT_ID", "CODE_RELAY_GITHUB_OAUTH_CLIENT_SECRET", "CODE_RELAY_SESSION_SECRET", "CODE_RELAY_GITHUB_APP_ID", "CODE_RELAY_GITHUB_APP_PRIVATE_KEY_FILE")) {
  if ($remoteEnvText -notmatch [regex]::Escape($name)) { throw "Hosted MCP environment example is missing $name" }
}

$task = Read-Json "examples/task-001.json"
foreach ($field in @("task_id", "source_commit", "target", "objective", "validation_plan", "expected_results")) {
  if ($null -eq $task.$field) { throw "Task example is missing $field" }
}
$receipt = Read-Json "examples/receipt-001.json"
foreach ($field in @("task_id", "source_commit", "status", "checks")) {
  if ($null -eq $receipt.$field) { throw "Receipt example is missing $field" }
}
if ($receipt.status -notin @("passed", "failed", "blocked")) {
  throw "Receipt example has an invalid status"
}

Write-Output "Code Relay contracts validated: $($schemaFiles.Count) schemas, plugin 2.0.0, MCP, task and receipt fixtures."
