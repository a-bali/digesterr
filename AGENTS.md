# AGENTS.md

## Project

Digesterr — feed reader with AI-generated digests. See `PLAN.md` for full spec.

## Tech stack

- **Backend:** Go (net/http router, sqlc for DB)
- **Frontend:** Vite + Vue + Tailwind (PWA) — not yet implemented
- **Database:** SQLite — must use standard SQL only, no SQLite-specific syntax
- **Auth:** JWT (golang-jwt/jwt/v5) + bcrypt, access tokens + refresh tokens
- **Dev environment:** devenv — run `devenv shell` to get Go, sqlc, nodejs, pnpm

## Commands

```bash
nix develop --no-pure-eval --accept-flake-config  # enter dev shell (all tools)
devenv shell             # enter dev shell (all tools, env vars set) — alternative to nix develop
devenv up                # start both servers (foreground, Ctrl+C to stop)
make generate            # run sqlc code generation
make build               # compile to bin/digesterr
make test                # go test ./...
make test-web            # pnpm --dir web test
make lint                # go vet ./...
make clean               # remove binary and DB file
```

### Dev loop (live reload, both sides)

Two standard tools, no scripts:

```bash
air                       # Go backend hot reload (watches .go/.sql, restarts on :8080)
pnpm --dir web dev        # vite HMR frontend (:10001, proxies /api -> :8080)
```

- **`http://hermes:10001` = vite dev server** — the URL to open. SPA with HMR,
  `/api` proxied to the Go backend on :8080 (internal, not host-visible)
- `.air.toml` uses `poll = true` (required on this container's overlayfs; harmless elsewhere)
- `.env` (gitignored) sets `PORT=8080`, `DATABASE_PATH`, `JWT_SECRET`, and provider secrets
- `vite.config.ts` bakes in `port: 10001`, `host: true`, `allowedHosts: ['hermes']`, and the `/api` proxy

### Prod build (frontend baked into the Go binary)

```bash
make build     # pnpm build -> cp web/dist cmd/digesterr/static -> go build (embeds via go:embed)
```

`cmd/digesterr/main.go` embeds `static/*` (`//go:embed static/*`). At runtime it serves
`web/dist` if present (dev fallback), otherwise the embedded files.

Use `nix develop --no-pure-eval --accept-flake-config` or `devenv shell` to enter a shell with all tools. Use `devenv up` to run both servers with env vars pre-set.

## Architecture

- **REST API** between Go server and Vue frontend
- **Feeds** are pluggable components — each feed type implements a common interface
- Item statuses: `unread`, `read`, `digested`, `saved`
- When a digest result is marked `read`, all its linked items transition from `digested` → `read`
- Digest configs store schedule + settings; digest results store the frozen config snapshot + generated content JSON
- `digest_result_items` join table links items to digest results with `is_highlight` flag
- LLM integration uses any OpenAI-compatible API (user supplies endpoint, key, model)
- DB schema is auto-applied on startup (embedded via `internal/schema/`). No migration tool — delete the DB file to reset.

## Key files

- `cmd/digesterr/main.go` — entrypoint, DB init
- `internal/auth/` — JWT, bcrypt, refresh tokens
- `internal/schema/` — embedded DB schema (single source of truth)
- `internal/store/` — sqlc-generated DB access (do not edit manually)
- `internal/httpapi/` — HTTP handlers and routing
- `internal/httpapi/reader*.go` — Google Reader API (mounted under `/reader/`)
- `internal/schema/schema.sql` — also used by sqlc for code generation (`sqlc.yaml` points here)
- `sql/queries/` — sqlc query definitions

## Logging

- Levels: `debug`, `info` (default), `warn`, `error`
- Configure via `LOG_LEVEL` env var or `-log-level` flag (flag wins over env)
- Backend logging infrastructure lives in `internal/logging/` (stdlib `log/slog`)
- Every emitted record carries a `category` key: `http`, `feed`, `item`,
  `digest`, `app` (plus `auth`). Categories are derived from the caller's
  source package; override with
  `slog.With(logging.CategoryAttr(logging.CategoryItem))`
- Records are also captured into an in-memory ring buffer served at
  `GET /api/logs` (size via `LOG_BUFFER_SIZE`)

## Google Reader API

Digesterr exposes a Google Reader–compatible API (mimicking Miniflux) under
`/reader/`, so any Google Reader client (Read You, FeedMe, Reeder,
NetNewsWire, …) can read digesterr.

- **Auth:** no username/password. Each user has an API key (`users.api_key`,
  generated at signup, returned as `"api_key"` in the signup response).
  Clients authenticate with `Authorization: GoogleLogin auth=<key>` or
  `X-Auth-Token: <key>`. Set the API endpoint base URL to
  `https://<host>/reader/`.
- **Feed mapping:** a feed → `feed/<id>`; a digest config → `digest/<id>`
  (digests appear as feeds); a digest result → `digest-item/<id>` (appears as
  an item in that feed).
- **State mapping:** item `digested` → read; item `saved` → starred (+read);
  digest result `new`/`read` → unread/read.
- **Endpoints:** `token`, `user-info`, `accounts/ClientLogin`,
  `subscription/list`, `subscription/export` (OPML), `unread-count`,
  `tag/list`, `stream/items/ids`, `stream/items/contents`,
  `stream/contents/<streamID>`, `edit-tag`, `mark-all-as-read`, plus no-op
  stubs for feed-management calls (`quickadd`, `subscription/edit`,
  `rename-tag`, `disable-tag`).
- Feed add/remove is intentionally not supported (stubbed as no-ops).

## Development

- After changing `sql/queries/*.sql` or `internal/schema/schema.sql`, run `make generate`
- To reset the database during development: `make clean` then restart the server
- Write tests after each iteration — use in-memory SQLite in tests (see `internal/httpapi/test_helpers.go`)

## Phased delivery

1. **Phase 1 (current):** Backend skeleton — auth, DB, REST API
2. **Phase 2:** Feed fetching (RSS/Atom, HN), content extraction
3. **Phase 3:** Basic UI (3-pane layout, feed browsing)
4. **Phase 4:** Digest generation, LLM integration, cron scheduling

## Running

```bash
devenv up
```

Or manually:

```bash
JWT_SECRET=your-secret DATABASE_PATH=digesterr.db PORT=8080 make run
```