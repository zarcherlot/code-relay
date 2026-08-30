param(
  [string]$Output = "dist/plugin"
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot "..")).Path
$outputRoot = if ([IO.Path]::IsPathRooted($Output)) { [IO.Path]::GetFullPath($Output) } else { [IO.Path]::GetFullPath((Join-Path $root $Output)) }
$rootPrefix = $root.TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
if ($outputRoot -eq $root -or -not $outputRoot.StartsWith($rootPrefix, [StringComparison]::OrdinalIgnoreCase)) {
  throw "Output must be a child directory of the repository: $outputRoot"
}
if (Test-Path -LiteralPath $outputRoot) {
  Remove-Item -LiteralPath $outputRoot -Recurse -Force
}
$binRoot = Join-Path $outputRoot "bin"
New-Item -ItemType Directory -Force $binRoot | Out-Null

$goos = if ($IsLinux) { "linux" } elseif ($IsMacOS) { "darwin" } else { "windows" }
$goarch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
  "X64" { "amd64" }
  "Arm64" { "arm64" }
  default { throw "Unsupported architecture: $([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture)" }
}
$name = "code-relay-agent-$goos-$goarch"
$binaryName = if ($goos -eq "windows") { "$name.exe" } else { $name }
$binaryPath = Join-Path $binRoot $binaryName
$previousEnvironment = @{
  CGO_ENABLED = $env:CGO_ENABLED
  GOOS = $env:GOOS
  GOARCH = $env:GOARCH
  GOCACHE = $env:GOCACHE
  GOMODCACHE = $env:GOMODCACHE
}

Push-Location $root
try {
  $manifest = Get-Content -LiteralPath (Join-Path $root ".codex-plugin/plugin.json") -Raw | ConvertFrom-Json
  $version = $manifest.version
  $env:CGO_ENABLED = "0"
  $env:GOOS = $goos
  $env:GOARCH = $goarch
  $env:GOCACHE = Join-Path $outputRoot ".build-cache/go"
  $env:GOMODCACHE = Join-Path $outputRoot ".build-cache/modules"
  go build -trimpath -ldflags "-s -w -X main.version=$version" -o $binaryPath ./cmd/code-relay-agent
  $canonicalName = if ($goos -eq "windows") { "code-relay-agent.exe" } else { "code-relay-agent" }
  $canonicalPath = Join-Path $binRoot $canonicalName
  Copy-Item -LiteralPath $binaryPath -Destination $canonicalPath -Force
  $versionOutput = (& $canonicalPath version).Trim()
  if ($versionOutput -ne "code-relay-agent $version") {
    throw "Plugin binary smoke test failed: $versionOutput"
  }

  $command = if ($goos -eq "windows") { "./bin/code-relay-agent.exe" } else { "./bin/code-relay-agent" }
  $mcp = @{ "code-relay" = @{ command = $command; args = @("mcp-stdio") } } | ConvertTo-Json -Depth 4
  Set-Content -LiteralPath (Join-Path $outputRoot ".mcp.json") -Value $mcp -Encoding UTF8
  Get-Content -LiteralPath (Join-Path $outputRoot ".mcp.json") -Raw | ConvertFrom-Json | Out-Null

  foreach ($path in @(".codex-plugin", "skills", "schemas", "templates")) {
    Copy-Item -LiteralPath (Join-Path $root $path) -Destination (Join-Path $outputRoot $path) -Recurse -Force
  }
  Write-Output "Plugin package: $outputRoot"
}
finally {
  $env:GOOS = $previousEnvironment.GOOS
  $env:GOARCH = $previousEnvironment.GOARCH
  $env:CGO_ENABLED = $previousEnvironment.CGO_ENABLED
  $env:GOCACHE = $previousEnvironment.GOCACHE
  $env:GOMODCACHE = $previousEnvironment.GOMODCACHE
  if (Test-Path -LiteralPath (Join-Path $outputRoot ".build-cache")) {
    Remove-Item -LiteralPath (Join-Path $outputRoot ".build-cache") -Recurse -Force
  }
  Pop-Location
}
