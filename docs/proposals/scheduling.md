# Proposal: Scheduling

Status: proposed. Covers phase 6 of the [build plan](../plan/README.md).

A read releases entries according to its **pacing**. There are two modes, and
a read can switch between them at any time.

## Pacing mode `rrule`: a fixed schedule

An iCalendar recurrence rule (RFC 5545), an IANA time zone and a count
released per occurrence:

| Intent | `rrule` | `per_release` |
|---|---|---|
| One a day at 07:00 | `FREQ=DAILY;BYHOUR=7;BYMINUTE=0;BYSECOND=0` | 1 |
| Three every weekday morning | `FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=7;BYMINUTE=0;BYSECOND=0` | 3 |
| Mon/Wed/Fri | `FREQ=WEEKLY;BYDAY=MO,WE,FR;BYHOUR=8;BYMINUTE=0;BYSECOND=0` | 1 |
| Two podcast episodes for the weekend | `FREQ=WEEKLY;BYDAY=FR;BYHOUR=17;BYMINUTE=0;BYSECOND=0` | 2 |

Use an established RRULE library rather than parsing by hand. The CLI may offer
shorthand (`--daily 07:00`, `--weekdays 07:00 --count 3`) that it turns into an
RRULE before calling the API; the API itself only accepts RRULEs.

Occurrences are evaluated in the schedule's time zone, so 07:00 stays 07:00
across daylight saving changes. A local time that does not exist on a DST day
(01:30 on the spring-forward day in Europe/Dublin) is handled as the RRULE
library and RFC 5545 define it; tests pin that behaviour down.

## Pacing mode `replay`: the original run

Entries are released with the same gaps between them as when they were first
published, divided by `speed`:

- `speed = 1`: the original timing. A blog read with `speed = 1` from its first
  post is that blog exactly as it ran, shifted to start now — "this site, N
  years ago".
- `speed = 7`: a weekly comic becomes a daily one, a fortnightly podcast
  arrives every two days, and the bursts and breaks of the original run (the
  hiatus, the week with three bonus episodes) are kept in proportion.

Each gap can be clamped with `min_gap` and `max_gap` (ISO 8601 durations), so
a two-year hiatus does not stall a read for months, and a day with ten posts
does not dump them all at once. Entries without an original date use
`default_gap` (or `max(min_gap, P1D)` if unset).

Replay needs original dates, so it suits feed-sourced serials and comics whose
pages carry dates. A probe reports what share of entries have dates, and the
API refuses `replay` on a source where too few do (threshold a flag) unless
`default_gap` is set.

## Segments: an append-only history

A read's position is never stored as a mutable counter. Instead a read has an
append-only list of **segments**. Each says: *starting at `starts_at`, with
`from_seq` already released, release entries according to this pacing* — or,
for `paused`, *release nothing*.

Every change appends a segment and never edits an old one. A segment's
`created_at` is when it takes effect; `starts_at` is what its pacing counts
from.

| Action | New segment |
|---|---|
| Create read | `from_seq = start_seq - 1 + initial` (default: the first entry is there immediately), `starts_at = now` |
| Change pacing | `from_seq = released(now)`, new pacing; `starts_at = now` for `rrule`, `release_time(from_seq)` for `replay` |
| Pause | `from_seq = released(now)`, `pacing = paused`, `starts_at = now` |
| Resume | `from_seq = released(now)`, previous pacing; `starts_at = now` for `rrule`, `now − (pause_start − release_time(from_seq))` for `replay` |
| Skip N / "next one now" | `from_seq = released(now) + N`, same pacing, `starts_at = now` |
| Jump to a position | `from_seq = chosen`, same pacing, `starts_at = now`. Refused with a 409 problem response if `chosen ≤ released(now)` |

Under `replay` a gap counts from the entry it follows, so a new segment anchors
at `release_time(from_seq)` rather than at `now`; if nothing has been released
yet, that is the read's creation time. Skip and jump release entries now, so
the next gap counts from now. Resume keeps the part of the current gap that had
already elapsed when the pause began (`pause_start`, the paused segment's
`starts_at`).

A read only moves forward. To read a serial again, create a new read: item IDs
are per read, so a reader shows the entries as new, where going back within a
read would repeat IDs it has already seen.

## The arithmetic

For time `t`, let `s` be the last segment, in append order, with
`created_at ≤ t`:

```
rrule:   due(t) = s.from_seq + s.per_release × occurrences(s.rrule, s.timezone, (s.starts_at, t])

replay:  release_at(q) = max(s.created_at, s.starts_at + Σ_{i = from_seq .. q-1} clamp(gap(i, i+1) / s.speed, min_gap, max_gap))
         due(t)        = max { q : release_at(q) ≤ t }

         where gap(i, i+1) = original_date(i+1) − original_date(i)
         (the first gap is measured from the last entry already released, so
          create and skip need no special case: their `initial`/skipped
          entries are inside from_seq already; the max() matters when an
          anchor is far enough back that a gap ended before the segment
          existed: those entries release together at created_at)

released(t) = min(due(t), end_of_index - buffer)
```

The **release time** of entry `q` is found by locating the segment in which
`q` first became released and computing which occurrence (rrule) or
cumulative gap (replay) did it. Entries released at the start of a segment
take the segment's `starts_at`. Because segments are never edited, an entry's
release time is stable for ever: feed items keep their timestamps and readers
never see them reorder or reappear.

Replay is O(entries since the segment started) per computation. Archives are
thousands of entries, not millions, so this is fine; if profiling says
otherwise, memoise per (segment, index length), which is safe because both are
immutable.

When `due(t)` runs past the end of the index:

- `catch_up = passthrough`: new entries are released as they are discovered,
  with release time `max(scheduled time, discovered_at)`.
- `catch_up = buffer`: the read holds `buffer` entries back from the end of the
  index, so there is always something in hand when the serial updates late.
  An entry's release time is `max(scheduled time, discovered_at of entry
  seq + buffer)`.

Downtime needs no special handling. If andante is off for a week, the next
request computes the correct position; the reader sees a week's entries, each
with its proper release time.

## Package shape

`internal/schedule` is pure: no database, no clock, no I/O. It takes segments,
an index view (length, and original dates for replay) and a time, and returns
positions and release times. It is the most heavily tested package in the
repository.

```go
type Pacing interface{ isPacing() }

type RRulePacing  struct{ RRule, TimeZone string; PerRelease int }
type ReplayPacing struct{ Speed float64; MinGap, MaxGap, DefaultGap time.Duration }
type Paused       struct{}

type Segment struct {
    FromSeq   int
    StartsAt  time.Time
    CreatedAt time.Time
    Pacing    Pacing
    Reason    Reason
}

type Index interface {
    End() int                          // highest seq indexed
    OriginalDate(seq int) (time.Time, bool)
    DiscoveredAt(seq int) time.Time
}

func Released(segs []Segment, t time.Time, idx Index) (int, error)
func ReleaseTime(segs []Segment, seq int, idx Index) (time.Time, error)
func NextRelease(segs []Segment, t time.Time, idx Index) (time.Time, bool, error)
```

## Tests

- Table tests for each action, in both pacing modes, and for switching between
  them mid-read.
- DST transitions in Europe/Dublin and America/New_York, both directions.
- Replay: speed 1 reproduces the original gaps exactly; clamps apply per gap;
  undated entries use `default_gap`; a hiatus longer than `max_gap` is clamped.
- Property tests (rapid or `testing/quick`), for both modes: `released` is
  monotonic in `t`; `ReleaseTime(q) ≤ t` iff `q ≤ released(t)`; appending a
  no-op segment (same pacing, `starts_at` as for "Change pacing") changes
  nothing; a pause followed straight away by a resume changes nothing; release
  times never change when a later segment is appended. The two "changes
  nothing" properties apply while `due(now) ≤ end_of_index - buffer`: a new
  segment's `from_seq` is `released(now)`, so anything owed past the end of the
  index is not carried over.
- A jump at or below `released(now)` is refused.
- Long horizons: a read created 10 years ago at 3/day computes quickly
  (occurrence counting must not iterate every occurrence when a closed form
  exists, and must be bounded when it does not).
