#!/usr/bin/env bash
# Dev frontend loop for digesterr (vite HMR).
# Serves the SPA on the host-visible port 10001 and proxies /api to the Go
# backend (port 8080, run via scripts/dev-backend.sh).
set -euo pipefail
cd "$(dirname "$0")/.."

pnpm --dir web dev --port 10001 --host
