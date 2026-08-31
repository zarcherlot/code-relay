$ErrorActionPreference = "Stop"
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot "..")).Path
$work = Join-Path ([IO.Path]::GetTempPath()) ("code-relay-e2e-" + [guid]::NewGuid().ToString("N"))
$repo = Join-Path $work "repo"
$binary = Join-Path $work $(if ($IsWindows -or $env:OS -eq "Windows_NT") { "code-relay-agent.exe" } else { "code-relay-agent" })
$oldCache = $env:GOCACHE
$oldModuleCache = $env:GOMODCACHE
$locationPushed = $false

function Invoke-Git([string[]]$arguments, [string]$directory) {
  $output = & git -C $directory @arguments 2>&1
  if ($LASTEXITCODE -ne 0) { throw "git $($arguments -join ' ') failed: $output" }
  return ($output -join "`n").Trim()
}

function Invoke-Agent([string[]]$arguments) {
  $output = & $binary @arguments 2>&1
  if ($LASTEXITCODE -ne 0) { throw "agent $($arguments -join ' ') failed: $output" }
  return ($output -join "`n").Trim()
}

New-Item -ItemType Directory -Force $repo | Out-Null
try {
  $env:GOCACHE = Join-Path $work "go-cache"
  $env:GOMODCACHE = Join-Path $work "go-mod-cache"
  Push-Location $root
  $locationPushed = $true
  try {
    $goos = if ($IsLinux) { "linux" } elseif ($IsMacOS) { "darwin" } else { "windows" }
    $goarch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
      "X64" { "amd64" }
      "Arm64" { "arm64" }
      default { throw "Unsupported architecture" }
    }
    $env:CGO_ENABLED = "0"
    $env:GOOS = $goos
    $env:GOARCH = $goarch
    go build -trimpath -ldflags "-s -w -X main.version=2.0.0" -o $binary ./cmd/code-relay-agent
  } finally {
    Pop-Location
  }

  Invoke-Git @("init") $repo | Out-Null
  Invoke-Git @("config", "user.name", "Code Relay E2E") $repo | Out-Null
  Invoke-Git @("config", "user.email", "code-relay-e2e@example.invalid") $repo | Out-Null
  Set-Content -LiteralPath (Join-Path $repo "README.md") -Value "Code Relay E2E" -Encoding utf8
  Invoke-Git @("add", "README.md") $repo | Out-Null
  Invoke-Git @("commit", "-m", "e2e fixture") $repo | Out-Null
  $commit = Invoke-Git @("rev-parse", "HEAD") $repo
  $task = @"
# Task
- task_id: e2e-smoke
- source_commit: $commit
- target: B
- objective: verify the local Code Relay end-to-end path

## Validation Plan
1. go version

## Expected Results
- command exits successfully

## Receipt Contract
- status and command output are recorded
"@
  $taskFile = Join-Path $work "task.md"
  Set-Content -LiteralPath $taskFile -Value $task -Encoding utf8
  Invoke-Agent @("publish", "--root", $repo, "--file", $taskFile, "--no-git") | Out-Null
  $pending = Invoke-Agent @("run-pending", "--root", $repo, "--timeout", "30") | ConvertFrom-Json
  if ($pending.Count -ne 1 -or $pending[0].status -ne "passed") { throw "run-pending did not pass: $pending" }
  $receipt = Invoke-Agent @("fetch-receipt", "e2e-smoke", "--root", $repo) | ConvertFrom-Json
  if ($receipt.status -ne "passed" -or $receipt.task_id -ne "e2e-smoke") { throw "receipt validation failed" }
  $analysis = Invoke-Agent @("analyze", "e2e-smoke", "--root", $repo) | ConvertFrom-Json
  if ($analysis.conclusion -ne "done") { throw "analysis did not conclude done" }

  $failureTask = $task.Replace("e2e-smoke", "e2e-failure").Replace("go version", "go version definitely-not-a-file")
  $failureFile = Join-Path $work "failure-task.md"
  Set-Content -LiteralPath $failureFile -Value $failureTask -Encoding utf8
  Invoke-Agent @("publish", "--root", $repo, "--file", $failureFile, "--no-git") | Out-Null
  $failedPending = Invoke-Agent @("run-pending", "--root", $repo, "--timeout", "30") | ConvertFrom-Json
  if ($failedPending.Count -ne 1 -or $failedPending[0].status -ne "failed") { throw "failure path did not produce failed receipt" }
  $failedAnalysis = Invoke-Agent @("analyze", "e2e-failure", "--root", $repo) | ConvertFrom-Json
  if ($failedAnalysis.conclusion -ne "iterate") { throw "failure path did not conclude iterate" }
  Write-Output "Code Relay local E2E smoke passed on $goos/$goarch."
} finally {
  if ($locationPushed) { Pop-Location -ErrorAction SilentlyContinue }
  $env:GOCACHE = $oldCache
  $env:GOMODCACHE = $oldModuleCache
  $env:GOOS = $null
  $env:GOARCH = $null
  $env:CGO_ENABLED = $null
  if (Test-Path -LiteralPath $work) { Remove-Item -LiteralPath $work -Recurse -Force }
}
