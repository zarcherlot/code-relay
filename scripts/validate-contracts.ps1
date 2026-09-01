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
  "schemas/runbook.schema.json"
)
foreach ($file in $schemaFiles) {
  $schema = Read-Json $file
  if (-not $schema.'$schema') {
    throw "Schema is missing `$schema: $file"
  }
}
$bindingSchema = Read-Json "schemas/binding.schema.json"
if ($bindingSchema.properties.schema_version.const -ne 2) {
  throw "Binding schema must use protocol version 2"
}
$policy = Read-Json "schemas/runtime-policy.json"
foreach ($field in @("allowed_commands", "denied_command_arguments", "deny_tokens", "shell_operators", "sensitive_env_keys")) {
  if ($null -eq $policy.$field) { throw "Runtime policy is missing $field" }
}

$manifest = Read-Json ".codex-plugin/plugin.json"
if ($manifest.name -ne "code-relay" -or $manifest.version -notmatch '^3\.1\.0(?:\+build\.[A-Za-z0-9._-]+)?$') {
  throw "Plugin manifest must identify code-relay 3.1.0, optionally with a build cachebuster"
}
if ($manifest.interface.displayName -ne "Code Relay") {
  throw "Plugin display name must be Code Relay"
}
$baseVersion = $manifest.version -replace '\+build\..+$', ''
$npmPackage = Read-Json "package.json"
$registryServer = Read-Json "server.json"
if ($npmPackage.name -ne "code-relay-mcp" -or $npmPackage.version -ne $baseVersion) {
  throw "npm package name/version does not match Code Relay $baseVersion"
}
if ($npmPackage.mcpName -ne "io.github.zarcherlot/code-relay") {
  throw "npm mcpName must use the authenticated GitHub namespace"
}
if ($registryServer.name -ne $npmPackage.mcpName -or $registryServer.version -ne $npmPackage.version) {
  throw "server.json name/version does not match package.json"
}
$registryPackage = @($registryServer.packages | Where-Object { $_.registryType -eq "npm" })
if ($registryPackage.Count -ne 1 -or $registryPackage[0].identifier -ne $npmPackage.name -or $registryPackage[0].version -ne $npmPackage.version -or $registryPackage[0].transport.type -ne "stdio") {
  throw "server.json must expose the exact npm package through stdio"
}
$installGuide = Join-Path $root "install.md"
if (-not (Test-Path -LiteralPath $installGuide -PathType Leaf)) { throw "Missing install.md" }
$installGuideText = Get-Content -LiteralPath $installGuide -Raw
foreach ($fragment in @("code-relay-mcp@latest", "--client codex", "--client claude-code", "--client cursor", "--client vscode", "--client generic", "--yes", "SHA256SUMS")) {
  if ($installGuideText -notmatch [regex]::Escape($fragment)) { throw "install.md is missing $fragment" }
}

$mcp = Read-Json ".mcp.json"
$server = $mcp.mcpServers.'code-relay'
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
foreach ($name in @("CODE_RELAY_GITHUB_OAUTH_CLIENT_ID", "CODE_RELAY_GITHUB_OAUTH_CLIENT_SECRET", "CODE_RELAY_SESSION_SECRET", "CODE_RELAY_GITHUB_APP_ID", "CODE_RELAY_GITHUB_APP_PRIVATE_KEY_FILE", "CODE_RELAY_PUBLIC_BASE_URL", "CODE_RELAY_OAUTH_RESOURCE_URL", "CODE_RELAY_OAUTH_ISSUER_URL", "CODE_RELAY_MCP_OAUTH_CLIENT_ID", "CODE_RELAY_MCP_OAUTH_REDIRECT_URIS", "CODE_RELAY_OAUTH_CODE_TTL", "CODE_RELAY_DATABASE_URL", "CODE_RELAY_REDIS_URL", "CODE_RELAY_SSE_HEARTBEAT_SECONDS", "CODE_RELAY_SSE_MAX_CONNECTIONS", "CODE_RELAY_SSE_MAX_QUEUE", "CODE_RELAY_SSE_EVENT_HISTORY")) {
  if ($remoteEnvText -notmatch [regex]::Escape($name)) { throw "Hosted MCP environment example is missing $name" }
}
$streamableContract = Read-Json "deploy/streamable-http-contract.json"
if ($streamableContract.local.transport -ne "stdio") {
  throw "Local transport contract must remain stdio"
}
if ($streamableContract.hosted.transport -ne "streamable-http" -or $streamableContract.hosted.endpoint -ne "/mcp") {
  throw "Hosted transport contract must use Streamable HTTP at /mcp"
}
if (($streamableContract.hosted.methods -join ",") -ne "POST,GET,DELETE") {
  throw "Hosted /mcp contract must expose POST, GET, and DELETE"
}
if (($streamableContract.hosted.responseMediaTypes -join ",") -ne "application/json,text/event-stream") {
  throw "Hosted /mcp contract must support JSON and SSE responses"
}
if ($streamableContract.hosted.session.idHeader -ne "Mcp-Session-Id" -or $streamableContract.hosted.session.resumeHeader -ne "Last-Event-ID") {
  throw "Hosted /mcp contract is missing session or replay headers"
}
$transportDoc = Join-Path $root "deploy/STREAMABLE-HTTP-CONTRACT.md"
if (-not (Test-Path -LiteralPath $transportDoc -PathType Leaf)) { throw "Missing Streamable HTTP contract" }
$transportDocText = Get-Content -LiteralPath $transportDoc -Raw
foreach ($fragment in @("stdio", "POST /mcp", "GET /mcp", "DELETE /mcp", "Mcp-Session-Id", "Last-Event-ID", "text/event-stream", "Protected Resource Metadata", "/oauth/authorize", "/oauth/token", "PostgreSQL", "Redis")) {
  if ($transportDocText -notmatch [regex]::Escape($fragment)) { throw "Streamable HTTP contract is missing $fragment" }
}
foreach ($legacyFragment in @("/sse", "/messages", "session_id=")) {
  if ($transportDocText -match [regex]::Escape($legacyFragment)) { throw "Streamable HTTP contract contains unsupported endpoint $legacyFragment" }
}
$proxyConfig = Join-Path $root "deploy/reverse-proxy-streamable-http.conf.example"
if (-not (Test-Path -LiteralPath $proxyConfig -PathType Leaf)) { throw "Missing Streamable HTTP reverse-proxy example" }
$proxyConfigText = Get-Content -LiteralPath $proxyConfig -Raw
foreach ($fragment in @("proxy_http_version 1.1", "proxy_buffering off", "proxy_read_timeout", "proxy_send_timeout", "X-Accel-Buffering no", 'proxy_set_header Connection ""')) {
  if ($proxyConfigText -notmatch [regex]::Escape($fragment)) { throw "Reverse proxy example is missing $fragment" }
}
$checkpointWorkflowPath = Join-Path $root ".github/workflows/checkpoint.yml"
if (-not (Test-Path -LiteralPath $checkpointWorkflowPath -PathType Leaf)) { throw "Missing checkpoint workflow" }
$checkpointWorkflow = Get-Content -LiteralPath $checkpointWorkflowPath -Raw
foreach ($fragment in @('runbooks/**', 'runbook_id:', 'jobs:', 'checkpoint:')) {
  if ($checkpointWorkflow -notmatch [regex]::Escape($fragment)) { throw "Checkpoint workflow is missing $fragment" }
}
foreach ($legacyFragment in @('tasks/**', 'task_id:', 'verify-on-b')) {
  if ($checkpointWorkflow -match [regex]::Escape($legacyFragment)) { throw "Checkpoint workflow contains legacy contract $legacyFragment" }
}
$publishWorkflow = Join-Path $root ".github/workflows/publish-mcp.yml"
if (-not (Test-Path -LiteralPath $publishWorkflow -PathType Leaf)) { throw "Missing npm/MCP Registry publication workflow" }
$publishWorkflowText = Get-Content -LiteralPath $publishWorkflow -Raw
foreach ($fragment in @("npm publish", "mcp-publisher validate server.json", "login github-oidc", "mcp-publisher publish server.json")) {
  if ($publishWorkflowText -notmatch [regex]::Escape($fragment)) { throw "Publication workflow is missing $fragment" }
}

$runbook = Read-Json "examples/runbook-001.json"
foreach ($field in @("runbook_id", "source_commit", "target", "objective", "validation_plan", "expected_results")) {
  if ($null -eq $runbook.$field) { throw "Runbook example is missing $field" }
}
$receipt = Read-Json "examples/receipt-001.json"
foreach ($field in @("runbook_id", "source_commit", "status", "checks")) {
  if ($null -eq $receipt.$field) { throw "Receipt example is missing $field" }
}
if ($receipt.status -notin @("passed", "failed", "blocked")) {
  throw "Receipt example has an invalid status"
}

Write-Output "Code Relay contracts validated: $($schemaFiles.Count) schemas, plugin $($manifest.version), npm $($npmPackage.version), MCP Registry, runbook and receipt fixtures."
