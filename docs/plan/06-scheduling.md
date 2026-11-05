# Phase 6 — Scheduling and reads

Read first: `CLAUDE.md`, `docs/philosophy.md` ("The feed is a function, not a
job"), `docs/proposals/scheduling.md` (all of it),
`docs/proposals/architecture.md` (reads, read_segments, the reads endpoints).

## Goal

A read can be created on a source with either pacing — a fixed RRULE schedule
or a replay of the original run's timing at a chosen speed; its position and
next release are computed from its segments; it can be paused, resumed,
skipped, jumped forward and re-paced, each by appending a segment.

## Do

- `internal/schedule`: the pure package described in the scheduling doc —
  `Pacing` (`RRulePacing`, `ReplayPacing`, `Paused`), `Released`,
  `ReleaseTime`, `NextRelease` — using an established RRULE library and an
  established ISO 8601 duration parser. No database, no `time.Now()`; times
  are parameters.
- Make occurrence counting efficient over long horizons (a read created ten
  years ago must compute in milliseconds); document the approach in the
  package doc.
- Tests per the scheduling doc: per-action tables in both pacing modes and
  switching between them, replay specifics (speed 1 reproduces original gaps,
  clamps, undated entries), DST in Europe/Dublin and America/New_York both
  directions (including a non-existent local time), property tests (monotonic, `ReleaseTime(q) ≤ t ⇔ q ≤ Released(t)`, appended
  segments never change earlier release times, a no-op segment changes nothing,
  a pause followed straight away by a resume changes nothing, a change to
  `replay` after a long quiet spell releases only the next entry), long-horizon
  performance, and `passthrough`/`buffer` behaviour at the end of the index.
  `Segment` carries both `StartsAt` and `CreatedAt`, and the store writes
  `created_at` from the `now` used to compute `from_seq`, never a column default.
- API: reads CRUD; `GET /reads/{id}` includes `position`, `due`,
  `next_release_at`, `index_end`, `state` (`active`/`paused`/`caught-up`);
  `POST /reads/{id}/adjustments` for `skip`, `pause`, `resume`, `jump` (409
  problem response if the seq is at or below `released(now)`), `pacing`
  (validates RRULE and time zone, or speed and gap durations; refuses
  `replay` on a source with too few dated entries unless `default_gap` is
  set); `GET /reads/{id}/segments`. Pacing in requests and responses is a
  `oneOf` discriminated by `mode`.
  Appending a segment happens in a transaction that locks the read row, so
  two concurrent adjustments serialise.
- Wire reads into discovery: the crawl loop's lookahead uses the furthest
  `due` position across a source's reads when it sets `sources.due_at`.
- `docs/reads.md`: the two pacing modes with examples (RRULEs; replay at
  speed 1 for "N years ago", speed 7 for weekly-to-daily, clamps for hiatuses),
  what each adjustment does, catch-up modes, what happens during downtime, and
  that a re-read is a new read because a read only moves forward.

## Out of scope

Feeds and tokens (phase 7). CLI shorthand for schedules (phase 8).

## Acceptance criteria

- [ ] `internal/schedule` coverage is high (aim for >95% of statements) and property tests run in CI with a fixed seed plus a random one logged on failure.
- [ ] Concurrent adjustments are tested against the real database.
- [ ] Discovery lookahead follows read positions (tested).

## Finish

Branch `phase-6-scheduling`. PR. Stop. Move `docs/proposals/scheduling.md` to
`docs/design/scheduling.md`.
