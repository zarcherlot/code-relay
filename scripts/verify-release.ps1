param(
  [string]$Path = "dist",
  [Parameter(Mandatory = $true)][string]$Version
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot "..")).Path
$dist = if ([IO.Path]::IsPathRooted($Path)) { [IO.Path]::GetFullPath($Path) } else { [IO.Path]::GetFullPath((Join-Path $root $Path)) }
if (-not (Test-Path -LiteralPath $dist -PathType Container)) { throw "Release directory does not exist: $dist" }

$expected = @(
  "code-relay-agent-linux-amd64",
  "code-relay-agent-linux-arm64",
  "code-relay-agent-windows-amd64.exe",
  "code-relay-agent-darwin-amd64",
  "code-relay-agent-darwin-arm64"
)
foreach ($name in $expected) {
  if (-not (Test-Path -LiteralPath (Join-Path $dist $name) -PathType Leaf)) { throw "Missing release artifact: $name" }
}

$manifestPath = Join-Path $dist "release.json"
if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) { throw "Missing release.json" }
$manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
if ($manifest.product -ne "Code Relay" -or $manifest.version -ne $Version -or $manifest.module -ne "github.com/zarcherlot/code-relay") {
  throw "Release manifest does not match Code Relay $Version"
}

$checksumPath = Join-Path $dist "SHA256SUMS"
if (-not (Test-Path -LiteralPath $checksumPath -PathType Leaf)) { throw "Missing SHA256SUMS" }
$seen = @{}
foreach ($line in Get-Content -LiteralPath $checksumPath) {
  if ([string]::IsNullOrWhiteSpace($line)) { continue }
  $parts = $line -split '\s+', 2
  if ($parts.Count -ne 2 -or $parts[0] -notmatch '^[0-9a-fA-F]{64}$') { throw "Invalid checksum line: $line" }
  $name = $parts[1].TrimStart('*', ' ')
  if ($name -eq "SHA256SUMS" -or $seen.ContainsKey($name)) { throw "Duplicate or recursive checksum entry: $name" }
  $file = Join-Path $dist $name
  if (-not (Test-Path -LiteralPath $file -PathType Leaf)) { throw "Checksum references missing file: $name" }
  $actual = (Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($actual -ne $parts[0].ToLowerInvariant()) { throw "Checksum mismatch: $name" }
  $seen[$name] = $true
}
foreach ($name in $expected) {
  if (-not $seen.ContainsKey($name)) { throw "Checksum missing release artifact: $name" }
}
Write-Output "Code Relay release artifacts verified: $Version ($($seen.Count) files)."
