# AGENTS.md

## Project

Digesterr — feed reader with AI-generated digests. See `PLAN.md` for full spec.

## Tech stack

- **Backend:** Go
- **Frontend:** Vite + Vue + Tailwind (PWA)
- **Database:** SQLite — must use standard SQL only, no SQLite-specific syntax, to allow future engine swap

## Dev environment

Uses **Nix devenv** for development environment setup.

## Architecture

- REST API between Go server and Vue frontend
- Feeds are **pluggable components** — each feed type (RSS/Atom, HN, Lobste.rs, Reddit) implements a common interface
- Item statuses: unread → read | digested | saved
- Digests are scheduled, LLM-summarized, and persisted to DB

## Development

- Write comprehensive tests after each iteration
- Follow Cursor directory best practices (https://cursor.directory)