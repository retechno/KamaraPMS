#!/usr/bin/env bash
# Runs golangci-lint (pinned version): scripts/lint.sh [extra golangci-lint args]
# Uses Docker when available, otherwise a local binary of the same version, otherwise `go run`.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
VERSION=v2.14.0

if docker info >/dev/null 2>&1; then
  MOUNT="$PWD"
  if command -v cygpath >/dev/null 2>&1; then MOUNT="$(cygpath -w "$PWD")"; fi   # Git Bash on Windows
  MSYS_NO_PATHCONV=1 exec docker run --rm -v "$MOUNT:/app" -w /app "golangci/golangci-lint:$VERSION" golangci-lint run "$@" ./...
elif command -v golangci-lint >/dev/null 2>&1 && golangci-lint version 2>/dev/null | grep -q "${VERSION#v}"; then
  exec golangci-lint run "$@" ./...
else
  exec go run "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$VERSION" run "$@" ./...
fi
