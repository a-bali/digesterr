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
nix develop              # enter dev shell (all tools) — for build/test/lint
devenv shell             # enter dev shell (all tools, env vars set) — alternative to nix develop
devenv up                # start dev server (foreground, Ctrl+C to stop)
make generate            # run sqlc code generation
make build               # compile to bin/digesterr
make test                # go test ./...
make test-web            # pnpm --dir web test
make lint                # go vet ./...
make clean               # remove binary and DB file
```

Use `nix develop` or `devenv shell` for build/test/lint. Use `devenv up` to run the server with env vars pre-set.

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
- `internal/schema/schema.sql` — also used by sqlc for code generation (`sqlc.yaml` points here)
- `sql/queries/` — sqlc query definitions

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