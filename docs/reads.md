# Reads

A read is a paced replay of a source -- the thing a feed serves. A source can have any number
of them. Where a read has got to is never stored. It's worked out from the read's history and
the time, so there's nothing to miss, run twice or repair.

## Creating a Read

```sh
curl -X POST -H "Authorization: Bearer $ANDANTE_TOKEN" -H 'Content-Type: application/json' \
  http://127.0.0.1:8080/api/v1/reads \
  -d '{"source_id": 1, "name": "Mornings", "pacing":
       {"mode": "rrule", "rrule": "FREQ=DAILY;BYHOUR=7;BYMINUTE=0;BYSECOND=0", "timezone": "Europe/Dublin"}}'
```

`start_seq` chooses where it starts, by default the source's first entry. `initial` is how many
entries are there straight away, by default one.

`GET /reads/{id}` reports:

| Field | Meaning |
|---|---|
| `position` | The last entry released |
| `due` | The last entry the pacing says is due, which can pass the index |
| `next_release_at` | When the next entry comes out, if that is known |
| `index_end` | The last entry indexed |
| `state` | `active`, `paused`, or `caught-up` at the end of the index |

## Pacing

### A Fixed Schedule

An iCalendar recurrence rule (RFC 5545), an IANA time zone, and how many entries each
occurrence releases. 07:00 stays 07:00 across daylight saving changes.

| Intent | `rrule` | `per_release` |
|---|---|---|
| One a day at 07:00 | `FREQ=DAILY;BYHOUR=7;BYMINUTE=0;BYSECOND=0` | 1 |
| Three every weekday morning | `FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=7;BYMINUTE=0;BYSECOND=0` | 3 |
| Mon/Wed/Fri | `FREQ=WEEKLY;BYDAY=MO,WE,FR;BYHOUR=8;BYMINUTE=0;BYSECOND=0` | 1 |
| Two episodes for the weekend | `FREQ=WEEKLY;BYDAY=FR;BYHOUR=17;BYMINUTE=0;BYSECOND=0` | 2 |

A rule can be at most hourly. A time that doesn't exist on a clock-change day moves an hour
later, and one that happens twice happens once, at the second.

### Replaying the Original Run

Entries come out with the gaps they first had, divided by `speed`:

```json
{"mode": "replay", "speed": 7, "min_gap": "PT12H", "max_gap": "P3D"}
```

- **`speed: 1`** is the original timing: "this site, N years ago".
- **`speed: 7`** turns a weekly comic into a daily one, and keeps the bursts and breaks of the
  original run in proportion.
- **`max_gap`** stops a hiatus from stalling the read. With `P30D`, a two-year break takes a
  month.
- **`min_gap`** stops a busy day from arriving all at once.
- **`default_gap`** stands in for a gap with an undated entry. Without it, that is one
  day, or `min_gap` if longer.

Gaps are ISO 8601 durations, in days or smaller. Replay needs dates, so it's refused on a
source where under `--replay-min-dated-share` of the entries have them, unless `default_gap` is
set. A probe's `dated_share` tells you beforehand.

## Adjustments

`POST /reads/{id}/adjustments` changes a read. Each one appends to its history, which
`GET /reads/{id}/segments` shows. Nothing gets rewritten, so entries already out keep their
timestamps.

| `type` | What it does |
|---|---|
| `skip` | Releases the next `count` entries now, stopping at the end of the index. |
| `pause` | Stops releases. |
| `resume` | Restarts them. Replay keeps the part of the current gap that had passed. |
| `jump` | Releases everything up to `seq` now. It must be ahead of the position and indexed. |
| `pacing` | Changes the pacing. At most one entry comes out at once, so a change does not release a backlog. |

A skip or a pause keeps a rule's rhythm: "every other day" stays on the same days.

### Reading Again

A read only moves forward. To read a serial again, create a new read. It has its own feed, so
your reader shows the entries as new, where going back within a read would repeat items it's
already seen.

## At the End of the Index

When a read catches up with its source:

- **`catch_up: passthrough`**, the default, releases new entries as they are discovered.
- **`catch_up: buffer`** holds back the last `buffer` entries, so there is always
  something in hand when the serial updates late.

These are fixed when the read is created, because release times depend on them. Make a new
read to change them.

## Downtime

Nothing to do. If andante is off for a week, the next request works out the right position,
and the week's entries appear with the times they were due.
