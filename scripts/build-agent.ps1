param(
  [string]$Output = "dist"
)
$ErrorActionPreference = "Stop"
New-Item -ItemType Directory -Force $Output | Out-Null
$targets = @(
  @{ GOOS = "linux"; GOARCH = "amd64"; Name = "code-relay-agent-linux-amd64" },
  @{ GOOS = "linux"; GOARCH = "arm64"; Name = "code-relay-agent-linux-arm64" },
  @{ GOOS = "windows"; GOARCH = "amd64"; Name = "code-relay-agent-windows-amd64.exe" }
)
foreach ($target in $targets) {
  $env:GOOS = $target.GOOS
  $env:GOARCH = $target.GOARCH
  $env:CGO_ENABLED = "0"
  go build -trimpath -ldflags "-s -w" -o (Join-Path $Output $target.Name) ./cmd/code-relay-agent
}
