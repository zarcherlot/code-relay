param(
  [Parameter(Mandatory = $true)][string]$Binary,
  [Parameter(Mandatory = $true)][string]$ProjectRoot,
  [string]$ServiceName = "CodeRelayAgent"
)
$ErrorActionPreference = "Stop"
$resolvedBinary = (Resolve-Path -LiteralPath $Binary).Path
$resolvedRoot = (Resolve-Path -LiteralPath $ProjectRoot).Path
$quoted = '"' + $resolvedBinary + '" daemon --root "' + $resolvedRoot + '" --role verifier --poll-interval 5'
if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
  Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
  sc.exe delete $ServiceName | Out-Null
  Start-Sleep -Seconds 1
}
New-Service -Name $ServiceName -DisplayName "Code Relay Agent" -Description "Code Relay verifier daemon" -BinaryPathName $quoted -StartupType Automatic
Start-Service -Name $ServiceName
Write-Output "Installed and started $ServiceName"
