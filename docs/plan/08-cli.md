# Phase 8 — The `andante` CLI

Read first: `CLAUDE.md`, `docs/proposals/architecture.md` ("Admin API (v1)"),
`docs/api.md`, `docs/reads.md`, `docs/feeds.md`.

## Goal

Everything the API can do is reachable from `andante`, which talks only to the
API through `pkg/client`.

## Do

- cobra command tree with shell completion (`andante completion bash|zsh|fish`).
  Connection settings from `--url`/`ANDANTE_URL` and
  `--token`/`ANDANTE_TOKEN`; no config file.
- Commands (nouns then verbs):
  - `source probe <url> [--selector field=css …]` — prints the entries found and
    the warnings; exits non-zero on errors.
  - `source add|list|get|edit|rm|crawl|entries`
  - `read create <source> [--start first|<seq>|<url>]` plus one pacing:
    - fixed: `[--daily HH:MM] [--weekdays HH:MM] [--days MO,WE,FR --at HH:MM]
      [--count N] [--rrule …] [--tz Europe/Dublin]` — shorthand compiles to an
      RRULE client-side; `--rrule` passes one through.
    - replay: `--replay [--speed 7] [--min-gap PT12H] [--max-gap P3D]
      [--default-gap P1D]`.
  - `read list|get|edit|rm|pause|resume|skip [N]|jump <seq>|pace …|history`
  - `token add <read> --label … [--owner]` — prints the feed URLs once.
  - `token list <read>|rm <id>`
  - `export opml --issue owner|viewer --label … > feeds.opml`
  - `import opml <file>`
  - `api-token add|list|rm`
  - `api spec` — prints `GET /api/v1/openapi.json`.
  - `version` (client version, and the server's when reachable).
- Output: human tables by default; `-o json` emits the API's JSON unchanged,
  for scripting.
- Errors: render Problem Details readably (`title`, `detail`, and field errors).
- Tests: drive commands against an `httptest` server running the real handlers
  and a test database; golden output for tables.
- `docs/cli.md`: install, connection settings, every command with an example,
  completions.

## Out of scope

Interactive TUI. Storing credentials on disk.

## Acceptance criteria

- [ ] Every API operation has a command (a test enumerates the spec's operationIds against the command tree).
- [ ] Shorthand schedule flags produce the expected RRULEs, and replay flags the expected pacing (table tests).
- [ ] `-o json` output is byte-identical to the API response body.

## Finish

Branch `phase-8-cli`. PR. Stop.
