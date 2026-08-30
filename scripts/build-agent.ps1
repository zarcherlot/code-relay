param(
  [string]$Output = "dist"
)
$ErrorActionPreference = "Stop"
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot "..")).Path
$outputRoot = if ([IO.Path]::IsPathRooted($Output)) { [IO.Path]::GetFullPath($Output) } else { [IO.Path]::GetFullPath((Join-Path $root $Output)) }
New-Item -ItemType Directory -Force $outputRoot | Out-Null
$targets = @(
  @{ GOOS = "linux"; GOARCH = "amd64"; Name = "code-relay-agent-linux-amd64" },
  @{ GOOS = "linux"; GOARCH = "arm64"; Name = "code-relay-agent-linux-arm64" },
  @{ GOOS = "windows"; GOARCH = "amd64"; Name = "code-relay-agent-windows-amd64.exe" },
  @{ GOOS = "darwin"; GOARCH = "amd64"; Name = "code-relay-agent-darwin-amd64" },
  @{ GOOS = "darwin"; GOARCH = "arm64"; Name = "code-relay-agent-darwin-arm64" }
)
$previousEnvironment = @{ CGO_ENABLED = $env:CGO_ENABLED; GOOS = $env:GOOS; GOARCH = $env:GOARCH; GOCACHE = $env:GOCACHE; GOMODCACHE = $env:GOMODCACHE }
Push-Location $root
try {
  $env:GOCACHE = Join-Path $outputRoot ".build-cache/go"
  $env:GOMODCACHE = Join-Path $outputRoot ".build-cache/modules"
  foreach ($target in $targets) {
    $env:GOOS = $target.GOOS
    $env:GOARCH = $target.GOARCH
    $env:CGO_ENABLED = "0"
    go build -trimpath -ldflags "-s -w" -o (Join-Path $outputRoot $target.Name) ./cmd/code-relay-agent
  }
  Get-ChildItem -LiteralPath $outputRoot -File -Filter "code-relay-agent-*" | Get-FileHash -Algorithm SHA256 | ForEach-Object {
    "$($_.Hash.ToLowerInvariant())  $($_.Path.Substring($outputRoot.Length + 1))"
  } | Set-Content -LiteralPath (Join-Path $outputRoot "SHA256SUMS") -Encoding ascii
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
