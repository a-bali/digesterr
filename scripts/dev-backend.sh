#!/usr/bin/env bash
# Dev backend loop for digesterr (replaces air on this container).
# Watches Go + SQL sources with entr; rebuilds and restarts on change.
# Frontend runs separately: `pnpm --dir web dev` (vite HMR).
set -euo pipefail
cd "$(dirname "$0")/.."

# Load .env if present
if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

# Initial build
echo "[dev] building..."
mkdir -p tmp
go build -o ./tmp/digesterr ./cmd/digesterr

# Watch sources; on change, entr -r kills the previous command and reruns it.
# NOTE: do NOT use `exec` here — entr -r needs to signal the shell's child.
find cmd internal sql -type f \( -name '*.go' -o -name '*.sql' \) \
  | entr -r -n sh -c 'echo "[dev] rebuilding..."; go build -o ./tmp/digesterr ./cmd/digesterr && ./tmp/digesterr'
