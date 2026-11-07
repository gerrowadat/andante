# Admin API

The API is the only way to change andante's state. It's on the main listener alongside the feeds,
under `/api/v1`. The contract is [`api/openapi.yaml`](../api/openapi.yaml), which a running server
also serves at `GET /api/v1/openapi.json` without a token.

## Authentication

Send an API token as a bearer token (RFC 6750):

```sh
curl -H "Authorization: Bearer $ANDANTE_TOKEN" http://127.0.0.1:8080/api/v1/sources
```

Every token belongs to a user, and `GET /me` says who. The first one comes from
[`andante-server user add`](configuration.md#users). Make more with `POST /api-tokens`. The raw
token only appears in that response. Revoking one that's already revoked succeeds. Your last
active token can't be revoked, because you'd be locked out until someone ran `andante-server user
reset`.

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
a source a read still uses, revoking your last active API token, changing a read's
`catch_up` or `buffer`, a jump to or behind the read's position or past the index, a
skip with nothing to skip to, pausing a paused read, or resuming one that is not.

### Catalogue Unavailable

Status 503. The request needs `andante-catalogue`, and it can't be reached. This covers
creating or changing sources, crawls, probes and OPML import. Reads, tokens and feeds carry on
without it, and so does listing sources.

### Forbidden

Status 403. You're logged in, but this needs an admin. Sources are shared, so only admins can change
or delete one.

### Unauthorized

Status 401, with a `WWW-Authenticate` header. The bearer token is missing, unknown or revoked.

## Who Sees What

Sources are shared. Anyone can list them, add one or ask for a crawl. `GET /sources` takes `q`, which
matches part of a title or slug, and `kind`.

Reads and tokens are yours. Someone else's is a 404, so that an ID tells you nothing. `GET /reads` means
your reads. An admin can reach anyone's, and `GET /reads?all=true` lists them all.

## Invites, Sessions and the Audit Log

`POST /invites` is for admins, and returns a single-use link, good for a week, where someone sets a
password. With `reset` it's for an existing user. `GET /sessions` lists your logged-in browsers, and
`DELETE /sessions/{id}` ends one. Someone else's session is a 404, even for an admin. `GET /audit` is
for admins.

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
curl -o openapi.json http://127.0.0.1:8080/api/v1/openapi.json
```
