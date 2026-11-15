# Proposal: More for the Website

What's left from [the website's design](../design/website.md) after its five phases.

## A Live Read Page

The Add page and the source page follow an event stream. The Read page doesn't. A read's position
moves with the clock as much as with the sync, so a `NOTIFY` from the sync isn't enough on its own:
the stream would also wake at the read's next release, and send the position and the next release
when either changes. Pause, resume, skip and jump already swap the summary in place.

| Stream | Path | Owner | What it sends |
|---|---|---|---|
| Read | `GET /reads/{id}/events` | the read's user | the position and the next release, when either changes |
