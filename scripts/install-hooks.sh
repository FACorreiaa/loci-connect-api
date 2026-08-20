#!/usr/bin/env bash
# Point this clone at the versioned hooks under .githooks/.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

git config core.hooksPath .githooks
chmod +x .githooks/pre-commit

echo "OK: core.hooksPath=.githooks (pre-commit runs gofumpt -w . and golangci-lint run)"
