#!/usr/bin/env bash
# Runs sqlc in Docker (no local install needed): scripts/sqlc.sh [generate|vet|diff]
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
MOUNT="$PWD"
if command -v cygpath >/dev/null 2>&1; then MOUNT="$(cygpath -w "$PWD")"; fi   # Git Bash on Windows
MSYS_NO_PATHCONV=1 docker run --rm -v "$MOUNT:/src" -w /src sqlc/sqlc:1.31.1 "${1:-generate}"
