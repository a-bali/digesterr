# Multi-stage build: web (vite) -> go (embedded static) -> slim alpine runtime.
#
# The binary embeds web/dist via go:embed (cmd/digesterr/main.go), so the
# runtime image needs nothing but the binary + CA certs (outbound LLM/Reddit).

# ---- Stage 1: web build ----
FROM node:22-alpine AS web
WORKDIR /src
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
# The static files must be present for go:embed to compile.
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/digesterr ./cmd/digesterr

# ---- Stage 3: runtime ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 1000 app
COPY --from=build /out/digesterr /usr/local/bin/digesterr
USER app
EXPOSE 8080
ENTRYPOINT ["digesterr"]
