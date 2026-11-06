# Design: Scheduling

Shipped in phase 6 of the [build plan](../plan/README.md). How to use it is in
[`reads.md`](../reads.md).

A read releases entries according to its **pacing**. There are two modes, and
a read can switch between them at any time.

## Pacing Mode `rrule`: a Fixed Schedule

An iCalendar recurrence rule (RFC 5545), an IANA time zone and a count
released per occurrence:

| Intent | `rrule` | `per_release` |
|---|---|---|
| One a day at 07:00 | `FREQ=DAILY;BYHOUR=7;BYMINUTE=0;BYSECOND=0` | 1 |
| Three every weekday morning | `FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=7;BYMINUTE=0;BYSECOND=0` | 3 |
| Mon/Wed/Fri | `FREQ=WEEKLY;BYDAY=MO,WE,FR;BYHOUR=8;BYMINUTE=0;BYSECOND=0` | 1 |
| Two podcast episodes for the weekend | `FREQ=WEEKLY;BYDAY=FR;BYHOUR=17;BYMINUTE=0;BYSECOND=0` | 2 |

It uses an established RRULE library rather than parsing by hand. The CLI has shorthand
(`--daily 07:00`, `--weekdays 07:00 --count 3`) that it turns into an RRULE before calling the
API, which only accepts RRULEs.

Occurrences are evaluated in the schedule's time zone, so 07:00 stays 07:00
across daylight saving changes. A local time that does not exist on a DST day
(01:30 on the spring-forward day in Europe/Dublin) is handled as the RRULE
library and RFC 5545 define it; tests pin that behaviour down.

## Pacing Mode `replay`: the Original Run

Entries are released with the same gaps between them as when they were first
published, divided by `speed`:

- `speed = 1`: the original timing. A blog read with `speed = 1` from its first
  post is that blog exactly as it ran, shifted to start now ("this site, N years ago").
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

## Segments: an Append-Only History

A read's position is never stored as a mutable counter. A read has an
append-only list of **segments**. Each says: *starting at `starts_at`, with
`from_seq` already released, release entries according to this pacing*, or,
for `paused`, *release nothing*.

Every change appends a segment and never edits an old one. A segment's
`created_at` is when it takes effect; `starts_at` is what its pacing counts
from.

| Action | New segment |
|---|---|
| Create read | `from_seq = start_seq - 1 + initial` (default: the first entry is there immediately), `starts_at = now` |
| Change pacing | `from_seq = released(now)`, new pacing; `starts_at = now` for a new `rrule`, `max(release_time(from_seq), now − next_gap)` for `replay` |
| Pause | `from_seq = released(now)`, `pacing = paused`, `starts_at = now` |
| Resume | `from_seq = released(now)`, previous pacing; `starts_at` as for a rule carrying on (below) for `rrule`, `now − (pause_start − release_time(from_seq))` for `replay` |
| Skip N / "next one now" | `from_seq = released(now) + N`, same pacing, `starts_at = now` |
| Jump to a position | `from_seq = chosen`, same pacing, `starts_at = now`. Refused with a 409 problem response if `chosen ≤ released(now)` |

A rule the read is already on carries on from its last occurrence, not from `now`,
whichever action appended the segment. "Every other day" is counted from a rule's
start, so restarting it now could move every later occurrence by a day, and a skip
has no business doing that. A jump and a skip are unchanged for `replay`.

Under `replay` a gap counts from the entry it follows, so a new segment anchors
at `release_time(from_seq)` rather than at `now`; if nothing has been released
yet, that is the read's creation time. Changing pacing limits how far back the
anchor can be: `next_gap` is the gap to entry `from_seq + 1` under the new
pacing (divided by `speed`, clamped, `default_gap` if undated), so after a long
quiet spell only that one entry is overdue, and it releases at `now`. Pace is
the product, so a change of pacing does not release a backlog. If that entry is
not indexed yet there is no `next_gap`, and the anchor is
`release_time(from_seq)`. Skip and jump release entries now, so the next gap
counts from now. Resume keeps the part of the current gap that had already
elapsed when the pause began (`pause_start`, the paused segment's `starts_at`).

A read only moves forward. To read a serial again, create a new read: item IDs
are per read, so a reader shows the entries as new, where going back within a
read would repeat IDs it has already seen.

## The arithmetic

For time `t`, let `s` be the last segment, in append order, with
`created_at ≤ t`, and let `end_of_index` count only what had been discovered by `t`.
Without that, an entry found later would count as released before andante knew of
it:

```
rrule:   due(t) = s.from_seq + s.per_release × occurrences(s.rrule, s.timezone, (s.starts_at, t])

replay:  release_at(q) = max(s.created_at, s.starts_at + Σ_{i = from_seq .. q-1} clamp(gap(i, i+1) / s.speed, min_gap, max_gap))
         due(t)        = max { q : release_at(q) ≤ t }

         where gap(i, i+1) = original_date(i+1) − original_date(i)
         (the first gap is measured from the last entry already released, so
          create and skip need no special case: their `initial`/skipped
          entries are inside from_seq already; the max() matters when a
          gap ended before the segment existed, as when a read waiting on
          its index resumes after the next entry was found: that entry
          releases at created_at)

released(t) = min(due(t), end_of_index - buffer)
```

The **release time** of entry `q` is found by locating the segment in which
`q` first became released and computing which occurrence (rrule) or
cumulative gap (replay) did it. Entries released at the start of a segment
take the segment's `starts_at`. Because segments are never edited, an entry's
release time is stable for ever: feed items keep their timestamps and readers
never see them reorder or reappear.

Replay is O(entries since the segment started) per computation. Archives are thousands of
entries, so this is fine. If profiling ever says otherwise, memoise per (segment, index length),
which is safe because both are immutable.

When `due(t)` runs past the end of the index:

- `catch_up = passthrough`: new entries are released as they are discovered,
  with release time `max(scheduled time, discovered_at)`.
- `catch_up = buffer`: the read holds `buffer` entries back from the end of the
  index, so there is always something in hand when the serial updates late.
  An entry's release time is `max(scheduled time, discovered_at of entry
  seq + buffer)`.

Downtime needs no special handling. If andante is off for a week, the next request computes the
right position, and the reader sees a week's entries, each with its proper release time.

## Package Shape

`internal/schedule` is pure: no database, no clock and no I/O. It takes segments, an index view
(length, and original dates for replay) and a time, and returns positions and release times.
It's the most heavily tested package in the repository.

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
    Start() int                        // lowest seq indexed
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
- Changing to `replay` after the gap to the next entry has elapsed releases that
  entry at `now` and no others, where the following gaps are above zero.
- Long horizons: a read created 10 years ago at 3/day computes quickly
  (occurrence counting mustn't iterate every occurrence when a closed form exists, and must be
  bounded when it doesn't).

## Where This Diverged From the Proposal

- **A rule keeps its rhythm across segments.** The property tests found this: with
  `INTERVAL=2`, a segment starting `now` on an odd day moved every later occurrence.
- **`reads.start_seq` is new.** Nothing else recorded the first seq a read covers,
  because the first segment's `from_seq` also counts the `initial` entries. Feeds
  need it.
- **Occurrences are counted in whole periods of 7 × INTERVAL days, anchored at local
  noon.** Anchoring at the segment's own time of day fails on a day when that time
  does not exist. The package doc has the reasoning.
- **`catch_up = buffer` is an `Index` wrapper,** `schedule.Buffered`. It shifts `End`
  and `DiscoveredAt`, so the arithmetic has no special case for it.
- **`Apply` is in the package,** so that every action is pure and table-tested, and
  the anchor rules live beside the arithmetic they feed.
- **`catch_up` and `buffer` cannot change after a read is created.** Release times
  depend on them, so a change would rewrite the past. `PATCH` refuses with a 409. The
  contract has both fields, and changing that would be a breaking change.
- **A skip stops at the end of the index, and a jump past it is refused,** so
  `from_seq` never passes what is indexed.
- **Rules more frequent than hourly are refused.** They are not a human pace, and
  they cannot be counted cheaply.
- **A repeated local time takes Go's choice, the second one.** Dublin's 01:30 on the
  day the clocks go back occurs once, an hour later than the first would be. Tests
  pin this down.
- **A rule's `COUNT` restarts with each segment,** and includes its start.
