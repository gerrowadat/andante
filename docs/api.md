# Admin API

The admin API is the only way to change andante's state. It's on the admin listener under
`/api/v1`. The contract is [`api/openapi.yaml`](../api/openapi.yaml), which a running server
also serves at `GET /api/v1/openapi.json` without a token.

## Authentication

Send an API token as a bearer token (RFC 6750):

```sh
curl -H "Authorization: Bearer $ANDANTE_TOKEN" http://127.0.0.1:8081/api/v1/sources
```

The first token comes from [bootstrap](configuration.md#the-bootstrap-api-token). Make more with
`POST /api-tokens`. The raw token only appears in that response. Revoking one that's already
revoked succeeds. The last active token can't be revoked, because then nothing could use the
API to make another.

API tokens are separate from feed tokens, which appear in feed URLs and get you nothing here.

## Errors

Every error is RFC 9457 problem details, as `application/problem+json`:

```json
{
  "type": "https://github.com/gerrowadat/andante/blob/main/docs/api.md#invalid-request",
  "title": "Bad Request",
  "status": 400,
  "detail": "the request does not match the API description",
  "errors": [{ "pointer": "/slug", "detail": "does not match pattern" }]
}
```

`errors`, when it's there, lists what was wrong with the request field by field, with a JSON
pointer into the body. A `type` of `about:blank` means the status says it all, as for a 404. The
other types are below.

### Invalid Request

Status 400. The request doesn't match the API description: a malformed body, an unknown field,
a missing field or a bad cursor.

### Invalid Config

Status 422. The request is well formed, but its source config can't be used, because the
strategy rejects a value. Configs are checked when they're written, so a bad one fails here and
not at crawl time.

### Invalid Read

Status 422. A read or an adjustment can't be used: a bad RRULE or time zone, a rule more
frequent than hourly, a gap measured in months, replay on a source with too few dated entries
and no `default_gap`, or a source that doesn't exist.

### Probe Failed

Status 422. A probe couldn't get an answer from the site: robots.txt disallows the page, the
site is refusing or failing, the links form a cycle, or the URL is a private address. `detail`
says which. A probe that gets some pages and then stops returns those, with a warning instead.

### Conflict

Status 409. The request clashes with what exists: a slug already in use, deleting
a source a read still uses, revoking the last active API token, changing a read's
`catch_up` or `buffer`, a jump to or behind the read's position or past the index, a
skip with nothing to skip to, pausing a paused read, or resuming one that is not.

### Unauthorized

Status 401, with a `WWW-Authenticate` header. The bearer token is missing, unknown or revoked.

## Importing OPML

`POST /imports/opml` takes an OPML file of at most 5 MiB and creates a paused `feed` source for
each outline. It answers 200 with a result per outline: `created`, `duplicate` or `invalid`. A
body that isn't OPML gets a 400.

## Probes and Crawls

`POST /probes` runs discovery without saving anything. It keeps to the same limits as crawling,
so it waits `--crawl-interval` between pages, and a probe of 10 pages takes about 45 seconds by
default. Set your client's timeout to match. The server allows it `--probe-timeout`, whatever
`--write-timeout` says.

`POST /sources/{id}/crawls` marks a source due, and the crawl happens in the background. See
[sources](sources.md).

## Updating

`PATCH` takes a JSON merge patch (RFC 7396), as `application/merge-patch+json`. Fields you leave
out stay as they are, and `null` clears a field that can be cleared. A source's `slug` can't
change. Its `state` can only be set to `paused` or `indexing`, because the crawler owns `tail`
and `error`.

## Pagination

Lists take `limit` (1 to 100, default 50) and `cursor`. A response carries a page of `items` and
a `next` link, which is `null` on the last page. If there's another page, the same link is in a
`Link` header (RFC 8288). Cursors are opaque, so follow the link instead of building one.

## Requests

Every response has an `X-Request-ID` header. It echoes the one you sent if that was usable, and
the server logs carry it for failed requests.

## Versioning

Within `/api/v1` changes are additive only. New endpoints and new optional fields can appear, so
ignore fields you don't know. Anything that would break a client needs `/api/v2`. CI runs
`oasdiff breaking` on every pull request to hold this.

## Generating a Client

Go programs can import `github.com/gerrowadat/andante/pkg/client`, which is what the CLI uses
too. For anything else, feed the spec to any OpenAPI 3.1 generator:

```sh
curl -o openapi.json http://127.0.0.1:8081/api/v1/openapi.json
```
