# Admin API

The admin API is the only way to change andante's state. It listens on the admin
listener under `/api/v1`. The contract is [`api/openapi.yaml`](../api/openapi.yaml),
which a running server also serves at `GET /api/v1/openapi.json` without a token.

## Authentication

Send an API token as a bearer token (RFC 6750):

```sh
curl -H "Authorization: Bearer $ANDANTE_TOKEN" http://127.0.0.1:8081/api/v1/sources
```

The first token comes from [bootstrap](configuration.md#the-bootstrap-api-token).
Make more with `POST /api-tokens`; the raw token appears only in that response.
Revoking one already revoked succeeds. The last active token cannot be revoked,
because nothing could then use the API to make another.

API tokens are separate from feed tokens, which appear in feed URLs and grant
nothing here.

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

`errors`, when present, lists what was wrong with the request field by field, with
a JSON pointer into the body. A `type` of `about:blank` means the status says it
all, as for a 404. The other types are below.

### Invalid request

Status 400. The request does not match the API description: a malformed body, an
unknown field, a missing field or a bad cursor.

### Invalid config

Status 422. The request is well formed, but its source config cannot be used: a
strategy that is not built yet, or a value the strategy rejects. Configs are
checked when they are written, so a bad one fails here and not at crawl time.

### Invalid read

Status 422. A read or an adjustment cannot be used: a bad RRULE or time zone, a rule
more frequent than hourly, a gap measured in months, replay on a source with too few
dated entries and no `default_gap`, or a source that does not exist.

### Probe failed

Status 422. A probe could not get an answer from the site: robots.txt disallows the
page, the site is refusing or failing, the links form a cycle, or the URL is a
private address. `detail` says which. A probe that gets some pages and then stops
returns them, with a warning instead.

### Conflict

Status 409. The request clashes with what exists: a slug already in use, deleting
a source a read still uses, revoking the last active API token, changing a read's
`catch_up` or `buffer`, a jump to or behind the read's position or past the index, a
skip with nothing to skip to, pausing a paused read, or resuming one that is not.

### Unauthorized

Status 401, with a `WWW-Authenticate` header. The bearer token is missing, unknown
or revoked.

### Not implemented

Status 501. The endpoint is in the contract but not built yet. The contract covers
all of v1 ahead of the code, so clients can be generated from it now.

## Probes and crawls

`POST /probes` runs discovery without saving anything. It keeps to the same limits
as crawling, so it waits `--crawl-interval` between pages and a probe of 10 pages
takes about 45 seconds by default. Set your client's timeout to match. The server
allows it `--probe-timeout`, whatever `--write-timeout` says.

`POST /sources/{id}/crawls` marks a source due, and the crawl happens in the
background. See [sources](sources.md).

## Updating

`PATCH` takes a JSON merge patch (RFC 7396), as `application/merge-patch+json`.
Fields you leave out stay as they are, and `null` clears a field that can be
cleared. A source's `slug` cannot change. Its `state` can only be set to `paused`
or `indexing`, because the crawler sets `tail` and `error`.

## Pagination

Lists take `limit` (1 to 100, default 50) and `cursor`. A response carries a page
of `items` and a `next` link, which is `null` on the last page. When there is
another page the same link is in a `Link` header (RFC 8288). Cursors are opaque, so
follow the link instead of building one.

## Requests

Every response has an `X-Request-ID` header. It echoes the one you sent if that was
usable, and the server logs carry it for failed requests.

## Versioning

Within `/api/v1` changes are additive only. New endpoints and new optional fields
can appear, so ignore fields you do not know. Anything that would break a client
needs `/api/v2`. CI runs `oasdiff breaking` on every pull request to hold this.

## Generating a client

Go programs can import `github.com/gerrowadat/andante/pkg/client`, which the CLI
uses too. For another language, feed the spec to any OpenAPI 3.1 generator:

```sh
curl -o openapi.json http://127.0.0.1:8081/api/v1/openapi.json
```
