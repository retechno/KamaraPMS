#!/usr/bin/env bash
# Runs sqlc: scripts/sqlc.sh [generate|vet|diff]
# Uses Docker when available (no local install), otherwise a local sqlc binary,
# otherwise `go run` (needs cgo and a C compiler, available on Linux sandboxes).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
VERSION=1.31.1
CMD="${1:-generate}"

if docker info >/dev/null 2>&1; then
  MOUNT="$PWD"
  if command -v cygpath >/dev/null 2>&1; then MOUNT="$(cygpath -w "$PWD")"; fi   # Git Bash on Windows
  MSYS_NO_PATHCONV=1 exec docker run --rm -v "$MOUNT:/src" -w /src "sqlc/sqlc:$VERSION" "$CMD"
elif command -v sqlc >/dev/null 2>&1; then
  exec sqlc "$CMD"
else
  CGO_ENABLED=1 exec go run "github.com/sqlc-dev/sqlc/cmd/sqlc@v$VERSION" "$CMD"
fi
