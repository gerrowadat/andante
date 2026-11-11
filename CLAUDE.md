# andante — Claude instructions

andante replays the archive of any serial — webcomics, podcasts, blogs, news
sites — as a paced feed. It indexes the archive (from its feed, or by crawling
its pages), releases entries on a schedule you choose or at the timing of the
original run, and serves the result as a feed (Atom, RSS 2.0, JSON Feed) that
any reader or podcast app can poll and that you can share with other people.

Webcomics are the first and most demanding case (their feeds rarely hold the
archive, so they need crawling), but nothing in the core may assume an entry is
a comic page. Kind-specific behaviour lives in extractors and decorators.

Read `docs/philosophy.md` before making design decisions, and the relevant
document in `docs/proposals/` (not built yet) or `docs/design/` (built) before
working on a component. `docs/prior-art.md` surveys existing tools — check
it before designing a feature. `docs/plan/` holds the phased build plan and the prompt
for each phase.

## Rules

**Always update tests.** Every code change has test coverage. No exceptions.
CI never makes a network request to a real website: crawler and extractor tests
run against fixture sites in `testdata/` served by `httptest`.

**A skipped test is not a passing test.** Tests that need Postgres skip
locally when `ANDANTE_TEST_DATABASE_URL` is unset, but CI sets
`ANDANTE_REQUIRE_DB=1`, which turns that skip into a failure. Do not add a
skip that CI cannot catch.

**Always update docs.** Docs are a set under `docs/` indexed by
`docs/README.md`; the top-level `README.md` stays a lean landing page.
- Server flag or env var added/changed → `docs/configuration.md` (flag *and* env var).
- API changed → `api/openapi.yaml` first, then regenerate, then the handler. The spec is the contract.
- Behaviour changed → the relevant guide in `docs/`.
- Design rationale → `docs/design/`; cross-cutting "why" → `docs/philosophy.md`.

**Proposals graduate to design docs when implemented.** `docs/proposals/` is
for not-yet-built ideas; `docs/design/` is the record of *why* a shipped
feature is the way it is. When a feature lands, `git mv` its proposal to
`docs/design/`, retitle it `# Design: …`, record what shipped and where it
diverged, and leave unbuilt parts behind in `docs/proposals/`. Do this in the
feature's own PR.

**Open standards first.** Before inventing a format, protocol, convention or
schema, find the open or common one and use it (see `docs/philosophy.md`,
"Open standards first", for the current list). If you deviate, write down why
in `docs/design/`.

**Do not re-implement incumbents.** Before writing a library or utility, check
for a well-established, actively maintained Go package that does the job and
use it.

**Generated code is never hand-edited.** `internal/api/gen/`, `pkg/client/`
and `internal/store/db/` are generated (oapi-codegen, sqlc). Change the spec or
the SQL and run `make generate`. CI fails if generated code is stale.

**API versions are a promise.** Within `/api/v1`, changes are additive only.
CI runs `oasdiff breaking` against `main`; a breaking change needs `/api/v2`,
not a waiver.

**Add Prometheus metrics for observable behaviour.** Anything that can fail, be
counted or be timed gets a metric, registered via `promauto.With(reg)` in the
constructor, under the `andante_` prefix. Tests use per-test registries.

**Do not merge PRs.** Branch, commit, push, open the PR — then stop.

**Never push directly to main.** All changes go through a branch and PR,
including docs and config-only changes. Branch from `origin/main`, not from
whatever is checked out.

**Write plain commit messages and PR descriptions.** What changed and why. No
superlatives, no "seamlessly", no "robust", no sales pitch. A PR description
reads like a code review.

**Every source file carries its SPDX header.** `SPDX-License-Identifier: Apache-2.0`
plus `Copyright (C) 2026 Dave O'Connor`, in the file's comment syntax. Source
files are `.go`, `.sh`, `.sql`, `.yml`, `.yaml`, `Makefile` and `Dockerfile`.
Markdown and generated files (`// Code generated … DO NOT EDIT.`) are exempt.
CI checks this.

## Keeping the token bill down

Most of the cost of a session is the context re-read on every turn, so fewer turns and a smaller
context matter more than shorter output.

- While working, run the tests for the package you're changing (`go test ./internal/web`), not the
  whole suite. Before a push, `scripts/check.sh` once. It prints one line per step and the failure
  for any that fail, so don't pipe it through `tail` or re-run it to read the output.
- CI is the backstop. After pushing, run `scripts/ci-wait.sh <pr>` in the background and stop. Don't
  poll `gh pr checks` or `gh run view`, and don't re-run locally what CI is already running.
- Read the part of a file you need (`grep -n`, then a line range), and don't re-read a file that's
  already in context.
- Start each phase or PR in a fresh session, with a prompt that names the plan doc. A fork carries
  the whole conversation with it, so use one only when that context is needed.
- PR descriptions say what changed and why, in a few lines. CI shows what passed, so they don't list
  tests or checks.

## Project layout

```
api/                      openapi.yaml (the API), catalogue.yaml (private), oapi-codegen configs
cmd/andante-server/       the user-facing service: reads, tokens, feeds, the API
cmd/andante-catalogue/    the infrastructure service: sources, entries, crawling
cmd/andante/              CLI binary (talks to the API only, via pkg/client)
internal/buildinfo/       version/commit/date via ldflags
internal/config/          server flags + env (kong)
internal/store/           the server's Postgres: migrations/, queries/, db/ (sqlc output)
internal/catalogue/       the catalogue's store/, api/ and generated client/
internal/replica/         the server's catalogue client, the sync and demand
internal/web/             the website: login, sessions, invites
internal/password/        Argon2id hashing and password rules
internal/clientip/        where a request really came from, for limits and the audit log
internal/httpapi/         what both APIs share: problems, validation, auth, metrics
internal/stacktest/       both services together, for tests
internal/discovery/       Strategy interface, crawler, politeness, strategies/
internal/extract/         page metadata extraction (JSON-LD, Open Graph, rel=next, selectors)
internal/schedule/        pacing (RRULE, replay) + segment math — pure, no I/O
internal/feed/            Atom, RSS 2.0 and JSON Feed rendering, decorators
internal/api/             API handlers; gen/ is oapi-codegen output
internal/server/          listeners, middleware, wiring, public feed routes
pkg/client/               generated API client (public: the CLI and other tools import it)
deploy/compose/           Docker Compose stack: andante and Postgres
deploy/nomad/             example Nomad job
deploy/prometheus/        example alert rules and their tests
test/e2e/                 end-to-end suite (build tag e2e)
testdata/sites/           fixture sites and feeds (comics, podcasts, blogs) for discovery tests
scripts/                  check.sh, ci-wait.sh, release.sh, build-dist.sh, license-check.sh, spec-diff.sh
```

## Key conventions

- The server is configured by flags with env var counterparts (`ANDANTE_*`),
  for bootstrap only. Everything the user configures lives in the database and
  is changed through the API. There are no config files.
- Two services, each with its own database. `andante-catalogue` knows about
  serials and has never heard of a user or a read. `andante-server` keeps a copy of
  sources and entries, and never reads the catalogue's tables. See
  `docs/design/architecture.md`.
- `andante-server` has two listeners: public (feeds and the API) and ops
  (`/healthz`, `/readyz`, `/metrics`). What has no auth is never mounted on the
  public listener.
- Anything a person owns goes through a store method that takes a `store.Actor`.
  Someone else's read is a 404. `store.Holder` is only for code that has already
  established the right, such as a feed request with a valid token.
- Public feed requests read the server's copy, and never call the catalogue or
  trigger a crawl.
- Strategy config is `jsonb`, decoded into a typed struct per strategy and
  validated on write, not at crawl time.
- Timestamps are RFC 3339 in UTC in storage and APIs; schedules carry an IANA
  time zone and are evaluated in it.
- Go 1.22+ ServeMux patterns (`GET /f/{file}`); no router library.
- `log/slog`, JSON in production.

## Vocabulary

| Term | Meaning |
|---|---|
| Catalogue | The service that holds sources and entries and does the crawling. |
| Source | A serial (comic, podcast, blog, news site), plus the strategy used to discover its archive. |
| Strategy | How a source's archive is discovered: `crawl`, `numeric`, `feed`, `feed-paged`. |
| Entry | One item of a serial — a comic page, an episode, a post — with a sequence number (`seq`) in reading order. |
| Kind | What an entry is: `page` (image-led), `episode` (has an audio/video enclosure), `article` (text-led). Decides which extractor and decorators apply. |
| Enclosure | A media file attached to an entry (URL, MIME type, length), as in RSS/Atom. Passed through, never rehosted. |
| Extractor | Pulls title, images, text, enclosures, date and next link out of a page or feed item. |
| Pacing | How a segment releases entries: `rrule` (fixed schedule) or `replay` (the original run's gaps, optionally sped up). |
| Probe | A dry-run discovery that returns the first N entries without saving anything. |
| Demand | How far readers have got through a source, as reported to the catalogue. It's what lazy indexing follows. |
| Tail mode | A source whose crawl has reached the live end and now only checks for new pages. |
| Read | A paced replay of a source: a schedule plus a position. The thing a feed serves. |
| Segment | One append-only row of a read's history: from this `seq`, at this time, under this pacing (or paused). |
| Release | An entry becoming visible in a read's feed. |
| Feed token | A secret in a feed URL. Role `owner` (feed + controls) or `viewer` (feed only). |
| API token | A bearer token for the API, belonging to a user. Separate from feed tokens. |
| User | Someone who owns reads. Sources are shared, reads are not. |
| Actor | Who a request is for, passed to the store. |
| Decorator | A step that adds content to a rendered feed item (image, origin, controls, links). |
