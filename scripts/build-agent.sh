#!/usr/bin/env bash
set -euo pipefail

output="${1:-dist}"
mkdir -p "$output"

targets=(
  "linux amd64 code-relay-agent-linux-amd64"
  "linux arm64 code-relay-agent-linux-arm64"
  "windows amd64 code-relay-agent-windows-amd64.exe"
  "darwin amd64 code-relay-agent-darwin-amd64"
  "darwin arm64 code-relay-agent-darwin-arm64"
)

for target in "${targets[@]}"; do
  read -r goos goarch name <<<"$target"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags "-s -w" -o "$output/$name" ./cmd/code-relay-agent
done

if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "$output"/code-relay-agent-* > "$output/SHA256SUMS"
elif command -v shasum >/dev/null 2>&1; then
  shasum -a 256 "$output"/code-relay-agent-* > "$output/SHA256SUMS"
else
  echo "sha256sum or shasum is required" >&2
  exit 1
fi
