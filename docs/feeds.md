# Feeds

Each read can be subscribed to as Atom, RSS 2.0 or JSON Feed, through tokens. A feed URL
is the only credential: anyone with it can read that feed, so treat it like a password.

## Subscribing

Issue a token:

```sh
curl -X POST -H "Authorization: Bearer $ANDANTE_TOKEN" -H 'Content-Type: application/json' \
  http://127.0.0.1:8081/api/v1/reads/1/tokens -d '{"role": "owner", "label": "me"}'
```

The response has the feed URLs and a `suggested` format: RSS for podcasts, which is what
podcast apps expect, and Atom otherwise. Paste a URL into your feed reader or podcast app.
**The token is shown only once.** Only its hash is stored, so it cannot be looked up later.

A feed holds the last 30 released entries. Set `{"window": 50}` in a read's `config` to
change that.

Items carry their **release time**, not the original date, so your reader files them as
new and not in 2009. The original date is in the item's text, with a link to the original
page. Images, audio and video load from the publisher's site.

## Sharing with friends

Give each person their own `viewer` token, with a label:

```sh
curl -X POST -H "Authorization: Bearer $ANDANTE_TOKEN" -H 'Content-Type: application/json' \
  http://127.0.0.1:8081/api/v1/reads/1/tokens -d '{"role": "viewer", "label": "Alex"}'
```

A group read is one read with several viewer tokens. Everyone sees the same entries at the
same moment. `GET /reads/{id}/tokens` shows when each token was last seen, so you can tell
who is still reading.

| Role | Gets |
|---|---|
| `owner` | The feed, and a "next one now" link on each item |
| `viewer` | The feed only |

To subscribe to everything at once, or hand someone a whole list, export OPML. Each export
issues a new token per read with the role and label you give, because existing tokens
cannot be read back:

```sh
curl -X POST -H "Authorization: Bearer $ANDANTE_TOKEN" -H 'Content-Type: application/json' \
  http://127.0.0.1:8081/api/v1/exports/opml -d '{"issue": "viewer", "label": "For Alex"}' > feeds.opml
```

## Revoking

```sh
curl -X DELETE -H "Authorization: Bearer $ANDANTE_TOKEN" http://127.0.0.1:8081/api/v1/tokens/3
```

That URL stops working at once, and it answers 404, the same as a URL that never existed.
Other tokens are unaffected, which is why everyone should have their own. Revoking does not
pull back what a reader already fetched.

## After a pacing change

Nothing already released moves. Entries keep their IDs and timestamps through a pacing
change, a skip or a pause, so a reader never sees items reorder or come back. Only what is
released from then on follows the new pacing.

## "Next one now"

An owner's feed has a "next one now →" link on each item. It opens a page with one button,
which releases the next entry for everyone on the read and takes you to its original page.

It takes two steps because feed readers and link previewers fetch links on their own, and
fetching the link must not release anything. The page lasts at least ten minutes.

## How often to poll

Feeds send `ETag` and `Cache-Control`, with `max-age` set to the time until the next release
and held between a minute and an hour. Each token gets `--feed-rate-limit` requests an hour
by default, and past that it gets a 429 with `Retry-After`. Feed requests never trigger a
crawl.
