# AGENTS.md

## Project

Digesterr — feed reader with AI-generated digests. See `PLAN.md` for full spec.

## Tech stack

- **Backend:** Go (net/http router, sqlc for DB)
- **Frontend:** Vite + Vue + Tailwind (SPA; PWA not implemented)
- **Database:** SQLite — must use standard SQL only, no SQLite-specific syntax
- **Auth:** JWT (golang-jwt/jwt/v5) + bcrypt, access tokens + refresh tokens
- **Dev environment:** devenv — run `devenv shell` to get Go, sqlc, nodejs, pnpm

## Commands

```bash
devenv shell             # enter dev shell (all tools, env vars set)
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

Use `devenv shell` to enter a shell with all tools. Use `devenv up` to run both servers with env vars pre-set.

## Architecture

- **REST API** between Go server and Vue frontend
- **Feeds** are pluggable components — each feed type implements a common interface
- Item statuses: `unread`, `read`, `digested`, `saved`
- When a digest result is marked `read`, all its linked items transition from `digested` → `read`
- Digest configs store schedule + settings (including `min_score`: a
  generation-time score threshold; items below it are excluded, items from
  non-scored feeds like plain RSS/Atom always pass); digest results store the
  frozen config snapshot + generated content JSON
- `digest_result_items` join table links items to digest results with `is_highlight` flag
- LLM integration uses any OpenAI-compatible API (user supplies endpoint, key, model)
- DB schema: `internal/schema/schema.sql` is applied on first start (embedded
  via `internal/schema/`). Existing databases are upgraded at startup by
  versioned migrations in `internal/migrate/` (each runs once inside a
  transaction, recorded in `schema_migrations`; a failure aborts startup).
  Add new migrations by appending to `internal/migrate/migrations.go` — never
  edit existing entries. To fully reset during development, delete the
  DB file (`make clean` removes both the repo-root and `data/` dev DBs).

## Key files

- `cmd/digesterr/main.go` — entrypoint, config load, graceful shutdown
- `internal/auth/` — JWT, bcrypt, refresh tokens
- `internal/migrate/` — versioned schema migrations (`schema_migrations`)
- `internal/schema/` — embedded DB schema (single source of truth)
- `internal/store/` — sqlc-generated DB access (do not edit manually)
- `internal/httpapi/` — HTTP handlers and routing
- `internal/httpapi/reader*.go` — Google Reader API (mounted under `/reader/`)
- `internal/oidc/` + `internal/httpapi/oidc.go` — optional OIDC sign-in
  (see `docs/oidc.md`; off by default, additive to local auth)
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

## Twitter/X rate limits

X's internal GraphQL enforces **per-endpoint** budgets in 15-minute windows and
advertises them on every response (`X-Rate-Limit-Limit`, `-Remaining`, `-Reset`).
Measured against live endpoints on 2026-09-10:

| Endpoint | Budget | Window |
| --- | --- | --- |
| `UserTweets` | 50 | 15 min |
| `UserByScreenName` | 150 | 15 min |

`UserTweets` is the binding constraint.

Rules that keep ingestion inside the budget:

- **One timeline fetch per feed per poll.** `TwitterFetcher` must implement
  `BulkEnrich`; without it `Poller.enrichForPoll` falls back to per-item
  `Enrich`, which re-resolves the user and re-fetches the whole timeline once
  per item — `2N + 4` requests per poll (44 for a 20-item timeline). This was
  the cause of the production 429s.
- **Screen-name → user ID is cached** for the process lifetime.
- **Score refresh yields to ingestion.** When remaining budget is low,
  opportunistic enrichment is skipped and only ingestion runs.
- On 429 the client blocks until `X-Rate-Limit-Reset` and raises a typed
  `metrics.StatusError` carrying 429, so the failure is recorded as `4xx`
  (not `500`) and is attributable to the feed in `GET /api/metrics`.

`GET /api/metrics` reports live `twitter_quota` state (limit, remaining,
reserve, blocked, measured).

## Development

- After changing `sql/queries/*.sql` or `internal/schema/schema.sql`, run `make generate`
- To reset the database during development: `make clean` then restart the server
- Write tests after each iteration — use in-memory SQLite in tests (see `internal/httpapi/test_helpers.go`)

## Phased delivery

All phases delivered: backend (auth, DB, REST API), feed fetching
(RSS/Atom, HN, Lobsters, Reddit), UI (3-pane layout, mobile), digest
generation with LLM integration and cron scheduling, content extraction,
and the Google Reader-compatible API.

## Running

```bash
devenv up
```

Or manually:

```bash
JWT_SECRET=your-secret DATABASE_PATH=digesterr.db PORT=8080 make run
```