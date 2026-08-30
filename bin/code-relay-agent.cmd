@echo off
setlocal
if defined CODE_RELAY_AGENT (
  "%CODE_RELAY_AGENT%" %*
  exit /b %errorlevel%
)
set "RELAY_ARCH=amd64"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" if exist "%~dp0..\dist\code-relay-agent-windows-arm64.exe" set "RELAY_ARCH=arm64"
if /I "%PROCESSOR_ARCHITEW6432%"=="ARM64" if exist "%~dp0..\dist\code-relay-agent-windows-arm64.exe" set "RELAY_ARCH=arm64"
set "RELAY_BINARY=%~dp0..\dist\code-relay-agent-windows-%RELAY_ARCH%.exe"
if not exist "%RELAY_BINARY%" (
  >&2 echo Code Relay agent not found: %RELAY_BINARY%
  >&2 echo Build it with scripts\build-agent.ps1 or set CODE_RELAY_AGENT.
  exit /b 1
)
"%RELAY_BINARY%" %*
