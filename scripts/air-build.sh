#!/usr/bin/env bash
# Air build script: rebuilds frontend + embeds it into the Go binary.
# Mirrors Makefile build-web + go build. Runs on any watched file change.
set -euo pipefail
cd "$(dirname "$0")/.."

# 1. Build the Vue frontend if it has sources.
if [ -d web/src ]; then
  echo "[air] building frontend..."
  (cd web && pnpm build)
  # 2. Copy dist into the Go embed dir.
  mkdir -p cmd/digesterr/static
  cp -r web/dist/. cmd/digesterr/static/
fi

# 3. Compile the Go binary (embeds static/).
echo "[air] building backend..."
go build -o ./tmp/digesterr ./cmd/digesterr
