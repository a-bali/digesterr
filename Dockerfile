# Multi-stage build: web (vite) -> go (embedded static) -> slim alpine runtime.
#
# The binary embeds web/dist via go:embed (cmd/digesterr/main.go), so the
# runtime image needs nothing but the binary + CA certs (outbound LLM/Reddit).

# ---- Stage 1: web build ----
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile
COPY web/ .
RUN pnpm build

# ---- Stage 2: go build ----
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The repo only carries a placeholder cmd/digesterr/static/.gitkeep — the
# go:embed in main.go embeds static/*, so put the real web build there before
# compiling. (web/dist is not copied to the runtime stage; the embedded files
# are the only static source in the container.)
COPY --from=web /src/web/dist ./cmd/digesterr/static/
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X digesterr/internal/httpapi.Version=${VERSION:-dev}" -o /out/digesterr ./cmd/digesterr

# ---- Stage 3: runtime ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 1000 app \
    && mkdir -p /data && chown app:app /data
COPY --from=build /out/digesterr /usr/local/bin/digesterr
# The default DB path is relative ("digesterr.db"), so it resolves against the
# working directory. Without a WORKDIR that is / , which app cannot write: the
# container dies at startup on a fresh volume with a permission error, and the
# mounted data directory is not where a relative path would look anyway.
WORKDIR /data
USER app
# The probe must follow the same port the server resolves, not a hardcoded 8080:
# a container on any other port read as unhealthy while serving traffic fine.
# SERVER_PORT and PORT are both honoured by config.applyEnv; a YAML server.port
# or the -port flag cannot be read from a static probe, so an operator who sets
# the port that way must pass one of these env vars too.
EXPOSE 8080
# /healthz is unauthenticated so cluster probes and this check both work.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- "http://127.0.0.1:${SERVER_PORT:-${PORT:-8080}}/healthz" || exit 1
ENTRYPOINT ["digesterr"]
