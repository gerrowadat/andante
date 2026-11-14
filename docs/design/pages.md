# Design: Pages

The website. A logged-in user can do everything the CLI can: browse and add sources, track one at a
pace, manage a read and its feed links, and look after their account. Admins also get the Users page
and can edit and delete sources. Login itself, with two-factor and OIDC, is in
[`login.md`](login.md).

## Pages

| Page | Path | What it does |
|---|---|---|
| Home | `/` | Your reads, with the pace, how far along each is and the next release |
| Browse | `/sources` | Every source, searchable by title and kind, with when it was last checked |
| Source | `/sources/{id}` | Details, last error, the first entries, your reads of it, a check-now button. Admins can edit and delete it |
| Track | `/sources/{id}/track` | Pick a pace and a name. Makes a read and an owner feed link, and goes to the read |
| Read | `/reads/{id}` | The pace in words, position, next release, feed links, pause, resume, skip, jump, change pace, delete |
| Add | `/add` | Paste an address, wait while andante looks, pick a candidate |
| Invites | `/invites` | Make invite links, see who's used them, withdraw them. For anyone who can invite |
| Users | `/users` | Make admin, disable, a new password link, delete, who invited whom, the invite settings. Admins only |
| Logs | `/logs` | What the server or the catalogue logged lately, by level, source or read. Admins only. See [logs](logs.md) |
| Account | `/account` | Change password, sessions, API tokens |
| Feed page | `/f/{token}` | For anyone with a feed link, no login: the source, the pace, the position and the next release |

The code is in `internal/web`: the route table in `pages_app.go`, the handlers in `handlers.go` and
`add.go`, and the templates and stylesheet embedded from `templates/` and `static/`. The feed page is
in `internal/server/public.go`, because it's part of the feeds.

## JavaScript

The pages are server-rendered `html/template`, and every one works with JavaScript off. On top of
that there's [htmx](https://htmx.org/) and one small `static/site.js` of our own, both served from
`static/`, and nothing inline. The Content-Security-Policy is:

```
default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' https: data:; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'
```

`img-src https:` is for source artwork, which is linked from the publisher and never copied, like
everything else. `data:` is for the icons Pico draws in checkboxes, radio buttons and selects, which
are SVGs in its stylesheet. There's no `unsafe-eval`, so anything in htmx that runs code from an
attribute (`hx-on`, trigger filters, `js:` values) is blocked, and we don't use it.

What htmx does:

- Pause, resume, skip and jump on the Read page post as before and follow the redirect, then swap
  `#summary` (the pace, the position and the controls), `#history` and `#status` out of the page that
  comes back. A refusal (a jump backwards, say) renders the whole page with the reason in `#status`,
  so it's swapped in the same way.
- Browse searches as you type, 300ms after the last key, swapping `#results` and pushing the
  `?q=` URL so a search can still be linked to.
- The Add page and the source page follow an event stream, through htmx 4's `hx-sse` extension.

Every element with `hx-get` or `hx-post` is a form whose `action` and `method` say the same thing,
so with JavaScript off it posts and redirects as it always did. The tests check that on every page
they render, and that there's no inline script and no `hx-on`.

`site.js` shows a copy button next to each feed link (only where the browser has a clipboard, which
means https or localhost), and when a request htmx made fails with something other than a page,
such as an expired session's plain-text 403, it puts the message in `#status` so the button doesn't
seem to do nothing. `#status` is where the layout puts errors and notices, and it's an
`aria-live` region so screen readers announce what lands there.

htmx's own config is in a `<meta name="htmx-config">`: `includeIndicatorCSS: false`, since
otherwise it adds a stylesheet of its own, and `history: "reload"`, so the back button loads the
page again and never restores one from a copy.

The Add page and the source page show progress as it happens, over Server-Sent Events, which is
below.

### htmx 4

The proposal was written against htmx 2. By the time it was built htmx 4 was the stable release,
and it suits us better: it uses `fetch`, doesn't keep copies of pages in `localStorage` (which would
have held feed links), and only evaluates code for the attributes we don't use. The settings the
proposal named (`allowEval`, `includeIndicatorStyles`) are htmx 2's and don't exist in 4. Attributes
aren't inherited in 4 unless you ask, so each form carries its own `hx-target` and `hx-select`.

## Stylesheet

The pages use [Pico CSS](https://picocss.com/), which styles plain HTML (`<nav>`, `<article>`,
`<table>`, forms) with few classes, and does light and dark from the browser's
`prefers-color-scheme`. `static/style.css` holds the little Pico doesn't do, using Pico's variables
for colours so that it follows the mode too. Pico's `.grid` puts related fields side by side, and
`<fieldset role="group">` joins an input to its button.

Pico and htmx are vendored in `static/vendor/`, with its version in the path, and `static/vendor.json` records
where each file came from and its SHA-256. `make vendor-web` downloads them and checks the hashes,
and a test fails if a file doesn't match or isn't listed. Because the path changes with the version,
vendored files are served as immutable for a year, and `style.css` and `site.js` for an hour. Dependabot can't
see vendored files, so the `vendor-web` workflow checks each one's GitHub releases on Mondays and
opens an issue when there's a newer one.

Between the two comes `static/theme.css`, the theme an admin picked, which sets Pico's variables.
It's made from a Design Tokens file, and [`website.md`](website.md#themes) says how.

## Pages and the API Share One Layer

The pages run in the same process as the API, and calling it over HTTP would need an API token for
every logged-in user. What they share instead is `internal/app`, which
holds the operations that are more than one store call: creating a read waits for the server's copy
of the catalogue to catch up and tells the catalogue about the new demand, deleting a source checks
nobody reads it, and so on. The API handlers and the page handlers both call it, so the rules live
in one place, and the website can't do anything the API can't. The philosophy's "the API is the
only way in" now says this.

## Who Can Reach What

Every page is in one table, and each row says whether it's for admins and what kind of thing its
`{id}` is: a read, a feed token, a session, an API token, a source or a user. One wrapper does the
checks that are the same everywhere:

- No session: a GET goes to `/login`, and a POST gets a 403.
- A POST needs the form's CSRF token, which is tied to the session.
- An admin page gets a 403 for anyone else.

The handlers then look things up as the logged-in user, so that someone else's read, token or
session is a 404, the same as in the API. `web.Routes()` exports the table, and the tests walk it:
every route without a login, every form with a bad token, every admin page as a user, and every
route that owns an ID with another user's ID in it. A new page is covered as soon as it says what it
owns, and the test fails if the table loses its owners.

## Browse Stays Local

Browse, Source and Home read only the server's copy of the catalogue. A slow or broken catalogue
can't take them down, and a test closes the catalogue and loads them. Checking for new entries and
adding a site do talk to the catalogue, and show the error when it's down.

## Adding a Site

Discovery takes a few seconds, sometimes longer, which is too long to hold a form submission open.
The Add page starts it in the background and sends you to a page that waits for it: with
JavaScript, it follows the look's event stream and shows the answer when it comes, and without, a
`<meta http-equiv="refresh">` inside `<noscript>` reloads it every few seconds.
The jobs are kept in memory, last an hour, and are private to whoever started them, and each user
can have three running. A restart loses them, which costs you pasting the address again.

A candidate that's already a source goes straight to that source. Close matches are listed as links
next to the candidate, with a note to check them first. If nothing turns up, the page asks for the
address of the very first page, and crawls forward from there.

## Live Progress

Two pages show things as they happen, each with a stream of its own in the route table, so the
login, ownership and route tests cover them like any other page:

| Stream | Path | Owner | What it sends |
|---|---|---|---|
| Discovery | `GET /add/{job}/events` | the job's user | the waiting part of the page, again at each step and when the look finishes, then `done` |
| Source | `GET /sources/{id}/events` | anyone logged in | the state, entry count and latest entry, the first entries, and the crawl log for admins |

Each event is HTML from the same template the page uses on first load (`look` in `found.html`,
`progress`, `first-entries` and `crawl-log` in `source.html`), so there's one way to render each
part. The source stream swaps the progress in place and the other two by ID, with `hx-swap-oob`.
A named event (`done`, or `gone` when a source is deleted) tells htmx to stop, through
`hx-sse:close`, and otherwise it reconnects by itself and gets the current state first.

A look at a site is in memory, so its job closes a channel when it changes, and the stream waits
on that. The server asks the catalogue for the look as a stream of its own (below), and each step
that comes back is a change: the page lists the steps so far, with a spinner on the one under way,
and once the answer's in they fold away under "How andante looked". A job keeps 50 steps at most.

Sources change when the sync applies something from the catalogue. `ApplyChanges` sends
`pg_notify('andante_source', ...)` once for each source a batch touched, inside its transaction, so
Postgres delivers it on commit and a listener never hears of a change before it can read it. This
works with several servers on one database, which an in-process broker wouldn't. A channel is heard
across the whole database, so the payload is the schema and the source's ID, and a listener ignores
other schemas'. Otherwise two copies of andante sharing a database would hear each other, as the
tests, each in a schema of their own, did. Each server holds
one `LISTEN` connection, taken out of the pool, and only while someone has a source page open. If
it loses the connection it gets it back with a growing wait, and every open page looks again, since
it could have missed something. A crawl brings changes in bursts, so the source stream waits a
second after hearing of one and renders once.

The details that make a stream last:

- The server's read and write timeouts would end it, so the handler clears the read deadline and
  sets a write deadline before each write, through `http.ResponseController`.
- A comment every 15 seconds keeps proxies from closing it, and `X-Accel-Buffering: no` stops
  nginx holding events back.
- Each person can have eight open on a server. Past that it's a 429, which `site.js` shows in
  `#status`.
- Streams end when the server starts shutting down, so they don't hold the shutdown up.

The metrics are `andante_web_streams` (open, by stream), `andante_web_stream_events_total`,
`andante_web_streams_refused_total` and `andante_web_listen_failures_total`.

The e2e suite drives Chrome with [chromedp](https://github.com/chromedp/chromedp) through the Add
page, once with JavaScript on (the answer shows without a reload, and nothing breaks the CSP) and
once with it off (the refresh gets there). CI sets `ANDANTE_REQUIRE_BROWSER`, so they can't skip
there. Locally they skip without a Chrome, or use the one in `ANDANTE_E2E_CHROME`.

### Where Live Progress Diverged

- **No read stream yet.** The proposal had one for the Read page, but its phase only named the Add
  and source pages, and a read's position moves with the clock as much as with the sync, which
  needs more than a `NOTIFY`.
- **The state is in words, and can't tell indexing from waiting.** The source page says
  "indexing", "reached the live end" or "parked", and the entry count and latest entry. Whether
  indexing has caught up with readers depends on the catalogue's lookahead, which the server
  doesn't know.
- **The listener is lazy.** It holds a connection only while a source page is open.

## Discovery Stages

`POST /discoveries` on the catalogue answers with Server-Sent Events when it's asked for them with
`Accept: text/event-stream`: a `stage` event as each step starts, then one `result`, with the same
body as the JSON answer, or one `error` with a problem. Without that header it answers as it always
did, so the public API and the CLI don't change. A request refused before it starts, such as one
with a bad URL, is still a plain problem with its status, since a stream has already said 200.

A stage is a `step` (`fetch`, `feed`, `feeds`, `paged` or `probe`) and a `message` in words, such as
"No feed is linked, so trying https://comic.example/feed.xml" or "Trying 2 of 3: its feed". Each
is a request to the site at least, and they're a crawl interval apart, which is why they're worth
showing. `internal/find` reports them through a callback, and counts them in
`andante_discovery_stages_total`, whether anyone's listening or not. The catalogue runs the look
while it writes the response, through a pipe to the generated code, which flushes each event, and
sends a comment every 15 seconds so a quiet stream doesn't look idle to anything in between.

`internal/replica` reads the stream with about forty lines of its own (`DiscoverStages`), since the
generated client can't. It only needs `event` and `data`, and skips ids, retries and comments, so a
library would have been more than the job. A catalogue that answers with JSON anyway still works.

### Where Discovery Stages Diverged

- **The events are documented in an `x-events` extension.** OpenAPI 3.1 can't describe what's in
  each event of a stream (3.2's `itemSchema` can), so the media type is a string, and `x-events`
  names the schema of each event. oapi-codegen can't see that, so the catalogue's client is
  generated without pruning, to keep `DiscoveryStage`.
- **The "first page" path has one stage of its own.** It's a probe, which doesn't stream, so the
  server says it's following the next links and waits.

## Pace

The pace form has presets: one a day, weekdays, chosen days, the original timing, and an RRULE box
for anything else. The CLI's shorthand for these moved to `pkg/pacing`, so that both use it.
`schedule.Describe` turns a pace into words ("Every weekday at 07:00 (UTC), 3 at a time"), for the
Read page, the feed page and the feed's own description (#30).

## The Feed Page

A feed URL with no extension (`/f/{token}`) is a page instead of a feed. It's for a friend who
opens their link in a browser. It shows the source, the pace, how many entries are out, the next
release and links to each format, and for an owner link, the "next one now" button. It shows
nothing the feed doesn't, and nothing about who else reads it. It has the website's stylesheets,
theme and all. The response has `no-referrer`, so the stylesheet requests don't carry the token,
`no-store`, and a CSP of `default-src 'none'; style-src 'self'; img-src data:`, with no scripts. A
revoked token gets a 404.

## Users

The proposal said little about managing users, so phase 14 added `GET /users`, `PATCH /users/{id}`
and `DELETE /users/{id}`, for admins, and `andante user list`, `user edit` and `user rm`.

- **Disabling someone** logs them out and stops their API tokens, but their feed links keep working,
  so that friends on a group read aren't cut off (#40). An admin can stop the links one by one.
- **Changing a role** ends all of that user's sessions. Phase 13 couldn't change roles, and
  [`login.md`](login.md) said this would rotate the session. Ending them is simpler, and the
  change of role is rare.
- **Deleting someone** removes their reads and tokens. The page asks first, because it can't be
  undone, and a read can't be handed to anyone else.
- **There's always an admin.** Demoting, disabling or deleting the last active one is a 409, and
  nobody can delete themselves.

## Where This Diverged From the Proposal

- **No htmx,** for the reasons above.
- **Browse is a plain table** of title, kind, state and last checked. The artwork and the first
  entries are on the Source page, which keeps Browse to one query.
- **Editing a source is title, kind and paused or running.** Selectors and the rest of the strategy
  config are still changed through the API or CLI, because a bad selector there is easier to fix
  with a probe beside you.
- **Feed links are on the Read page,** not a page of their own.
- **The presets are one a day, weekdays and chosen days,** in place of "three a week", which is
  chosen days with three of them.
- **Close matches are a note, not a separate step.** The candidate's Add button is right below it.
- **Account has two-factor but no linked logins.** Linking a provider goes through a reset invite.
  See [`login.md`](login.md).
