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
# The probe asks the binary itself, which resolves the port the same way the
# server does: the port recorded at startup (covering CLI flags and YAML), then
# the config file, then SERVER_PORT/PORT, then the built-in default. Reading the
# env vars alone made a container serving on a YAML or -port port report
# unhealthy, so an operator who set the port that way had to duplicate it as an
# env var anyway. /healthz is unauthenticated, so the probe needs no credentials.
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["digesterr", "healthcheck"]
ENTRYPOINT ["digesterr"]
