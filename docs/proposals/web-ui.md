# Proposal: A Better Website

Status: phases 1 (the stylesheet), 2 (htmx and the CSP) and 3 (live progress) are built, and
described in [pages.md](../design/pages.md), which also says where they differ from this. The rest
is proposed.

The website works, but it looks like a 2003 admin panel and it behaves like one. The worst of it is
the Add page, which says "this page checks again every few seconds" and reloads itself with a
`<meta http-equiv="refresh">` until discovery finishes. After you add a source there's nothing at
all: you go to its page and reload it yourself to see whether the crawl has found anything.

This proposal does three things:

- Shows progress live, without reloading the page: discovery while it looks, then indexing once a
  source is added.
- Gives the site a proper stylesheet, with light and dark modes and themes you can swap.
- Uses established libraries for both, so we aren't writing a front-end framework of our own.

It reverses one recorded decision: [`pages.md`](../design/pages.md) says the site has no
JavaScript. That was right while the only dynamic thing was one waiting page. It isn't now.

## What Stays the Same

- Pages are rendered on the server with `html/template`, from the same handlers, through
  `internal/app`. No single-page app, no JSON-to-DOM layer, and no second copy of the rules in
  JavaScript.
- Every page still works with JavaScript off. Forms keep their `action` and `method`, links stay
  links, and the live parts fall back to what they do today.
- No Node, no npm and no build step. `go build` still produces the whole thing.
- Nothing is loaded from a CDN. Everything is embedded and served by `andante-server`.

## Choices

### Interaction: htmx

[htmx](https://htmx.org/) lets an element fetch HTML from the server and swap it into the page,
driven by attributes (`hx-get`, `hx-post`, `hx-target`). That fits how the site is already built:
the handlers render templates, and htmx asks for the part of a page it needs. It's one file of
around 15 KB gzipped, has no dependencies, is BSD-licensed and is widely used with Go servers.

Its [SSE extension](https://htmx.org/extensions/sse/) connects to a
[Server-Sent Events](https://html.spec.whatwg.org/multipage/server-sent-events.html) stream and
swaps each event's HTML into the page. That's the live progress, with no code of ours in the
browser.

Considered:

- **Datastar.** A newer library built around SSE, with client-side state as well. Good fit, but
  younger and smaller than htmx, and we don't need the client-side state.
- **Alpine.js.** Client-side state and behaviour. We don't need it, and the standard build uses
  `new Function`, which needs `unsafe-eval` in the CSP.
- **React, Svelte, Vue.** A build step, a JSON API for the site to talk to, and the rules in two
  places. Far too much for this.
- **Hand-written `fetch` and `EventSource` code.** Small to start with, and then it grows into
  the thing this proposal is trying not to write.

### Live updates: Server-Sent Events

SSE is part of the HTML standard, goes over a plain HTTP response, sends the session cookie like
any other request, and reconnects by itself. WebSockets would be two-way, which we don't need, and
need a library on the server. Polling with `hx-trigger="every 2s"` would work but is the meta
refresh with better manners.

### Styling: Pico CSS

[Pico CSS](https://picocss.com/) styles plain semantic HTML (`<nav>`, `<article>`, `<form>`,
`<table>`, `<details>`) with few or no classes, which suits templates that are already mostly plain
HTML. It's MIT-licensed, around 10 KB gzipped, has light and dark modes built in, and everything it
draws comes from CSS custom properties (`--pico-primary`, `--pico-background-color` and so on), so a
theme is a set of variables and not a second stylesheet.

Considered:

- **Tailwind.** Needs its CLI to build the stylesheet, and puts the styling in class lists in
  every template, which makes a theme a matter of editing templates.
- **Bootstrap.** Heavier, and wants its classes and markup everywhere.
- **Open Props.** Good design tokens, but tokens only. We'd still write the components.

### Templates: stay on `html/template`

[templ](https://templ.guide/) would give type-checked components, and is the usual pairing with
htmx in Go. There are 16 templates and about 400 lines of them, and `html/template` can already
render one named block as a fragment for htmx to swap. Not worth the extra code generator for now.
If the templates grow a lot, it's the first thing to look at again.

### Themes: Design Tokens

A theme is a [Design Tokens](https://www.designtokens.org/tr/drafts/format/) file, the W3C
community group's JSON format, which tools like Figma plugins and Style Dictionary read and write.
andante takes a small fixed set of tokens (colours, font family, radius, spacing) and maps each to
Pico's variables. Anything outside that set is refused, and values are checked by type (a colour
has to parse as a colour), so a theme can't inject arbitrary CSS.

The themes are built in: Design Tokens files embedded in the binary, checked when the server starts.
There's one theme for the whole site, which an admin picks as a new field in `/api/v1/settings`
(in `openapi.yaml` first) and on the Users page's settings. Every page uses it, the public feed
page included. Light or dark follows the browser's `prefers-color-scheme`, which Pico does on its
own, so there's no per-user setting.

The server renders each theme to `/static/themes/{name}.css` with an ETag, so it's still
`style-src 'self'`.

Per-user themes and themes an admin uploads are left for later. Using the tokens format now means
an uploaded theme would be the same kind of file as a built-in one, checked the same way.

## Shipping the Libraries

htmx, its SSE extension and Pico go in `internal/web/static/vendor/`, embedded like the rest of
`static/`. A `vendor.json` next to them records each file's version, source URL and SHA-256, and
`make vendor-web` downloads them and checks the hashes. CI checks that the files match the hashes.
Dependabot can't see vendored files, so a weekly workflow asks each project's GitHub releases for
anything newer than `vendor.json` has and opens an issue if there is.

The files are served with a long `Cache-Control` and a version in the path, so an upgrade isn't
stuck behind old caches.

## Content Security Policy

The policy gains `script-src 'self'` and nothing else:

```
default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' https: data:; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'
```

`data:` images weren't in the plan. Pico draws its checkbox, radio and select icons as SVG `data:`
URLs, and phase 1 added it to the policy for them.

No inline scripts and no `unsafe-eval`. htmx needs two settings for that, set in a
`<meta name="htmx-config">`: `allowEval: false`, and `includeIndicatorStyles: false` (otherwise it
adds an inline `<style>`). `hx-on` attributes need eval, so we don't use them; the few bits of
behaviour htmx doesn't cover (a "copy feed link" button) go in one small
`site.js` of our own.

The test that fails on any `<script>` changes to fail on any inline script or `hx-on`, and on any
form that has `hx-post` but no `action`.

## Live Progress

### On the server

One SSE handler per page that has something live, each a normal row in the route table with an
owner, so the existing tests cover login, CSRF and ownership for them as they do for every page:

| Stream | Path | Owner | What it sends |
|---|---|---|---|
| Discovery | `GET /add/{job}/events` | the job's user | each stage, then the candidates |
| Source | `GET /sources/{id}/events` | anyone logged in | entry count, state, last checked, new crawl log lines (admins) |
| Read | `GET /reads/{id}/events` | the read's user | the position and the next release, when either changes |

Each event's data is a rendered template fragment, the same one the page uses on first load, so
there's one template per part of the page and no second rendering path.

Where events come from:

- **Discovery** jobs are already in memory in `internal/web`. They get a list of stages and a way
  to wait for the next change.
- **Sources and reads** change when the replica sync applies something from the catalogue. The
  sync sends a Postgres `NOTIFY` with the source's ID after it applies a change, and the stream
  handlers `LISTEN`. That works with more than one `andante-server` sharing a database, which an
  in-process broker wouldn't. pgx supports both.

Things to get right, all well known:

- The server's write timeout would cut a stream off, so the handler extends its deadline with
  `http.ResponseController` as it goes.
- A comment line every 15 seconds keeps proxies from closing an idle stream, and
  `X-Accel-Buffering: no` stops nginx holding events back.
- Each user gets a limit on open streams, the same idea as the three running discoveries today.
- Metrics: `andante_web_streams` (open, by stream), `andante_web_stream_events_total` and
  `andante_web_streams_refused_total`.

### In the browser

The Add page shows the stages as they happen and the candidates as soon as they're ready. Once you
pick one, it takes you to the source's page, which shows the indexing as it goes: how many entries
so far, the latest one, whether it's still crawling, caught up to where readers are, or reached the
live end. Admins see the crawl log grow.

Without JavaScript, the waiting page keeps its meta refresh inside `<noscript>`, which is valid in
`<head>`, so it does what it does today. Pages with htmx don't get it.

### Stages from the catalogue

Today `POST /discoveries` on the catalogue is one call that can take minutes and then answers, so
the server knows "started" and "done" and nothing in between. For real stages ("fetching the home
page", "found an Atom feed", "trying candidate 2 of 3"), the catalogue streams them: the same
request with `Accept: text/event-stream` gets a `stage` event as each happens and a `result` event
with the same body as the JSON answer. Without that header it behaves as it does now, so the CLI is
unaffected. `catalogue.yaml` is private, but this is additive anyway.

The generated client won't read a stream, so `internal/replica` gets a small reader for this one
call. There are maintained Go SSE parsers if ours gets more than a few lines.

## Other Improvements That Come Cheap

Once htmx is there:

- Pause, resume, skip and jump on the Read page swap the read's summary in place, without a full
  reload. Without JavaScript they still post and redirect.
- Browse filters as you type (`hx-trigger="input changed delay:300ms"`), keeping the `?q=` URL so
  results stay linkable.
- Notices and errors appear in a status area that screen readers announce.
- Feed links get a copy button.

## Testing

- Handler tests check the fragments and read the event streams with `httptest`, against the
  stacktest catalogue, which already serves fixture sites.
- The route-table tests cover the new stream routes like any other route.
- Two e2e tests drive a real browser with [chromedp](https://github.com/chromedp/chromedp), the
  usual Go library for headless Chrome: adding a fixture site with JavaScript on (progress shows
  without a reload), and the same with JavaScript off (the meta refresh still works). Both go
  behind the existing `e2e` build tag.
- A test fails if a vendored file doesn't match `vendor.json`.

## Phases

Each is one PR.

1. **Stylesheet and layout.** Vendor Pico, rewrite `layout.html` and `style.css` on top of it, go
   through the templates, with light and dark following the browser. No JavaScript yet.
2. **htmx and the CSP.** Vendor htmx, change the policy and its tests, and do the cheap
   improvements above. `pages.md` gets its "No JavaScript" section rewritten to say why there is
   some now.
3. **Live progress.** The stream handlers, `NOTIFY` from the sync, the Add page and the source
   page. The meta refresh moves into `<noscript>`.
4. **Discovery stages.** The streaming discovery on the catalogue and its reader in the server.
5. **Themes.** A few built-in themes as Design Tokens files, and the site theme in
   `/api/v1/settings`, in `openapi.yaml` first.

Phases 1 and 2 are mostly visible to people using it. 3 is the one that gets rid of the reload.
