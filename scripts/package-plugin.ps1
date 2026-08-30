param(
  [string]$Output = "dist/plugin"
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot "..")).Path
$outputRoot = Join-Path $root $Output
$binRoot = Join-Path $outputRoot "bin"
New-Item -ItemType Directory -Force $binRoot | Out-Null

$goos = if ($IsLinux) { "linux" } elseif ($IsMacOS) { "darwin" } else { "windows" }
$goarch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq "Arm64") { "arm64" } else { "amd64" }
$name = "code-relay-agent-$goos-$goarch"
$binaryName = if ($goos -eq "windows") { "$name.exe" } else { $name }
$binaryPath = Join-Path $binRoot $binaryName

Push-Location $root
try {
  $env:CGO_ENABLED = "0"
  $env:GOOS = $goos
  $env:GOARCH = $goarch
  $env:GOCACHE = Join-Path $root ".gocache"
  $env:GOMODCACHE = Join-Path $root ".gomodcache"
  go build -trimpath -ldflags "-s -w" -o $binaryPath ./cmd/code-relay-agent
  $canonicalName = if ($goos -eq "windows") { "code-relay-agent.exe" } else { "code-relay-agent" }
  Copy-Item -LiteralPath $binaryPath -Destination (Join-Path $binRoot $canonicalName) -Force

  $command = if ($goos -eq "windows") { "./bin/code-relay-agent.exe" } else { "./bin/code-relay-agent" }
  $mcp = @{ "code-relay" = @{ command = $command; args = @("mcp-stdio") } } | ConvertTo-Json -Depth 4
  Set-Content -LiteralPath (Join-Path $outputRoot ".mcp.json") -Value $mcp -Encoding UTF8

  foreach ($path in @(".codex-plugin", "skills", "schemas", "templates")) {
    Copy-Item -LiteralPath (Join-Path $root $path) -Destination (Join-Path $outputRoot $path) -Recurse -Force
  }
  Copy-Item -LiteralPath (Join-Path $root ".mcp.json") -Destination (Join-Path $outputRoot ".mcp.source.json") -Force
  Write-Output "Plugin package: $outputRoot"
}
finally {
  $env:GOOS = $null
  $env:GOARCH = $null
  $env:CGO_ENABLED = $null
  $env:GOCACHE = $null
  $env:GOMODCACHE = $null
  Pop-Location
}
