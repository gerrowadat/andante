# Phase 3 — Server skeleton and store

Read first: `CLAUDE.md`, `docs/philosophy.md`, `docs/proposals/architecture.md`
("Components", "Configuration", "Data model", "Metrics").

## Goal

`andante-server` starts, migrates Postgres, serves health and metrics on the admin
listener, serves nothing yet on the public listener, and creates a bootstrap
API token.

## Do

- `internal/config`: kong struct with every flag in the architecture doc's
  configuration table, each with its `ANDANTE_*` env var, defaults, and
  validation (URLs parse, durations positive, time zone loads).
- `internal/server`: two `http.Server`s with sensible timeouts
  (`ReadHeaderTimeout` etc.), graceful shutdown on SIGINT/SIGTERM, slog
  request logging middleware, panic recovery. Admin mux: `/healthz` (process
  alive), `/readyz` (database reachable and migrations applied), `/metrics`
  (Prometheus, a per-server registry). Public mux: empty for now, 404 for
  everything.
- `internal/store`: pgx pool; goose migrations in `migrations/` embedded via
  `embed.FS`, run on start under a Postgres advisory lock; the full v1 schema
  from the architecture doc in the first migration; sqlc config
  (`sqlc.yaml` — this is tool config, not app config) with queries in
  `queries/` generating into `db/`. Only the queries this phase needs.
- `make generate` runs sqlc. CI's `contracts` job (add it now) runs
  `make generate` and fails on a diff.
- API token bootstrap: if `api_tokens` is empty, use `--admin-token` if given
  (store its hash) or generate one and log it once at WARN with a clear
  message. Tokens are 32 random bytes, base64url, stored as SHA-256.
- Test database helper: `storetest.New(t)` creates an isolated schema (or
  database) per test from `ANDANTE_TEST_DATABASE_URL`, migrates it, and drops
  it on cleanup. If the env var is unset it skips — unless
  `ANDANTE_REQUIRE_DB=1`, in which case it fails.
- CI: add a `postgres` service to the `test` job and set both env vars. Add
  `make test-db` that starts `compose.yaml` Postgres and runs tests with them
  set.
- `make run`: start `andante-server` against the compose database with text logs.
- `docs/configuration.md`: the flag/env table, plus "how config works" (bootstrap
  flags only; everything else through the API).
- Metrics: `andante_build_info{version,commit}` gauge and the standard Go/process
  collectors. Others arrive with their features.

## Out of scope

The admin API itself (phase 4). Crawling, reads, feeds.

## Acceptance criteria

- [ ] Fresh database: `make run` migrates, prints a bootstrap token once, `/readyz` returns 200.
- [ ] Two `andante-server` processes started together against one database both come up; migrations run once.
- [ ] Tests cover config validation, migration idempotency, token bootstrap (both paths), readiness failing when the DB is down, and shutdown.
- [ ] CI runs the DB tests (no skips); generated sqlc code is checked.

## Finish

Branch `phase-3-server-store`. PR. Stop.
