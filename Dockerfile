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
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/digesterr ./cmd/digesterr

# ---- Stage 3: runtime ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 1000 app
COPY --from=build /out/digesterr /usr/local/bin/digesterr
USER app
EXPOSE 8080
ENTRYPOINT ["digesterr"]
