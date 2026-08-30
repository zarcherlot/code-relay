param(
  [string]$Output = "dist",
  [string]$Version = "1.0.0"
)
$ErrorActionPreference = "Stop"
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot "..")).Path
$outputRoot = Join-Path $root $Output
New-Item -ItemType Directory -Force $outputRoot | Out-Null
$targets = @(
  @{ GOOS = "linux"; GOARCH = "amd64"; Name = "code-relay-agent-linux-amd64" },
  @{ GOOS = "linux"; GOARCH = "arm64"; Name = "code-relay-agent-linux-arm64" },
  @{ GOOS = "windows"; GOARCH = "amd64"; Name = "code-relay-agent-windows-amd64.exe" },
  @{ GOOS = "darwin"; GOARCH = "amd64"; Name = "code-relay-agent-darwin-amd64" },
  @{ GOOS = "darwin"; GOARCH = "arm64"; Name = "code-relay-agent-darwin-arm64" }
)
Push-Location $root
try {
  foreach ($target in $targets) {
    $env:GOOS = $target.GOOS
    $env:GOARCH = $target.GOARCH
    $env:CGO_ENABLED = "0"
    go build -trimpath -ldflags "-s -w -X main.version=$Version" -o (Join-Path $outputRoot $target.Name) ./cmd/code-relay-agent
  }
  $env:GOOS = $null
  $env:GOARCH = $null
  $env:CGO_ENABLED = $null
  $manifest = @{
    product = "Code Relay"
    version = $Version
    module = "github.com/zarcherlot/code-relay"
    built_at_utc = (Get-Date).ToUniversalTime().ToString("o")
    files = @($targets | ForEach-Object { $_.Name })
  } | ConvertTo-Json -Depth 4
  [IO.File]::WriteAllText((Join-Path $outputRoot "release.json"), $manifest + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
  $sbom = @{
    bomFormat = "CycloneDX"
    specVersion = "1.5"
    version = 1
    metadata = @{ timestamp = (Get-Date).ToUniversalTime().ToString("o"); component = @{ name = "code-relay"; version = $Version } }
    components = @(
      @{ type = "application"; name = "code-relay-agent"; version = $Version; purl = "pkg:golang/github.com/zarcherlot/code-relay@$Version" },
      @{ type = "application"; name = "code-relay"; version = $Version; purl = "pkg:pypi/code-relay@$Version" }
    )
  } | ConvertTo-Json -Depth 8
  [IO.File]::WriteAllText((Join-Path $outputRoot "sbom.cdx.json"), $sbom + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
  Get-ChildItem -LiteralPath $outputRoot -File | Where-Object { $_.Name -ne "SHA256SUMS" } | Get-FileHash -Algorithm SHA256 | ForEach-Object {
    "$($_.Hash.ToLowerInvariant())  $($_.Path.Substring($outputRoot.Length + 1))"
  } | Set-Content -LiteralPath (Join-Path $outputRoot "SHA256SUMS") -Encoding ascii
}
finally {
  Pop-Location
}
