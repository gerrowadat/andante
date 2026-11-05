# Prior art

Tools that replay an archive as a feed, and what andante takes from or does
differently to each. Check this before designing a feature; someone has often
tried it.

| Tool | Scope | Notes |
|---|---|---|
| [Comic Rocket — Archive Binge](https://www.comic-rocket.com/help/archive-binge) | Webcomics, hosted | The closest to andante's original use case: a per-comic RSS feed at a chosen rate (up to 10 a day, chosen weekdays), starting anywhere. Uses its own crowd-sourced index of comic pages rather than the comic's feed. Hosted and poorly maintained in recent years; not self-hostable. |
| [FeedRewind](https://github.com/ilidemi/feedrewind) | Blogs, hosted + source | Go and Postgres, web and worker processes. Discovers the full list of posts for an arbitrary blog and serves a feed that starts at the beginning on a schedule. **Study its blog-archive discovery** before building andante's article strategies; it has solved many of the same problems. |
| Feed Playback / Stream Spigot (Mihai Parparita, 2011) | Blogs, comics | Early hosted version of the idea: a new feed updated daily from an existing feed's archive. Relied on Google Reader's feed history, which is gone. |
| [regulated-reader](https://github.com/wolfgang42/regulated-reader) | Webcomics, script | Follows "next" links one page per run; pace set by cron. The crawl strategy in its simplest form. |
| [dripfeed](https://pypi.org/project/dripfeed/) | Webcomics, script | Same idea, Python, state in a dotfile, run from cron. |
| [rss-reruns](https://github.com/hannahlog/rss-reruns) | Any feed with a full archive | Rebroadcasts old items to a new feed, shuffled or in order, with the original date in the title. Stores its own state inside the output XML under a custom namespace. |
| [feed-repeat](https://github.com/abhin4v/feed-repeat) | Any feed | Haskell, self-hosted. Spaced-repetition reposting of old posts, not ordered replay. |
| [blog-replay](https://github.com/steadmon/blog-replay) | WordPress/Blogger | Rust; scrapes a blog into a local DB and replays posts one by one into an Atom feed. |
| [rss-replay](https://github.com/mobeets/rss-replay) | Any feed | Rebuilds a feed's history from Wayback Machine snapshots of the feed. |
| [history4feed](https://pypi.org/project/history4feed/) | Blogs | Builds a full-text historical archive of a feed from Wayback Machine snapshots. |
| feedburst (Rust) | Webcomics, reader-side | Rations comics inside a reader with rules like "on Mondays" or "every N days". Pace on the reading side; no output feed. |

## What andante does differently

- **One system for every kind of serial.** Comics need crawling, podcasts
  rarely do, blogs sit in between. The prior art mostly picks one; andante
  makes discovery a pluggable strategy and keeps the core kind-agnostic.
- **Replay of the original run's timing**, not only a fixed rate.
- **Deterministic, append-only pacing.** Several tools advance a stored counter
  on a cron; a missed or doubled run shifts everything. andante computes the
  position from immutable segments.
- **Shareable per-person feeds** for reading together, with revocation.
- **An API-first, open-standards service** (OpenAPI, RRULE, Atom/RSS/JSON
  Feed, OPML), rather than a script or a closed hosted service.

## Ideas worth borrowing later

- A `wayback` discovery strategy (rss-replay, history4feed): rebuild a
  feed's archive from Wayback Machine snapshots when neither the feed nor the
  site exposes it. Respect the Internet Archive's rate limits.
- FeedRewind's heuristics for finding a blog's full post list (archive pages,
  sitemaps, paginated indexes).
- rss-reruns' option to show the original date in the item title.
