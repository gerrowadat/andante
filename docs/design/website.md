# Design: A Better Website

The website worked, but it looked like a 2003 admin panel and behaved like one. The Add page said
"this page checks again every few seconds" and reloaded itself with a `<meta http-equiv="refresh">`
until discovery finished, and after you added a source you went to its page and reloaded it yourself
to see whether the crawl had found anything.

This was a proposal in five phases, and all five shipped:

1. **The stylesheet** (#77). Pico CSS, with light and dark following the browser.
2. **htmx and the CSP** (#78). htmx, `script-src 'self'`, and the small things htmx makes cheap.
3. **Live progress** (#79). Server-Sent Events for the Add page and the source page, and `NOTIFY`
   from the sync.
4. **Discovery stages** (#80). The catalogue streams each step of a look at a site.
5. **Themes**. Built-in themes as Design Tokens files, picked in `/api/v1/settings`.

[`pages.md`](pages.md) describes the website as it is: the JavaScript, the stylesheet, live progress
and discovery stages, each with where it differed from the plan. This is the record of why the
pieces were chosen, and of the themes.

It reversed one recorded decision: `pages.md` used to say the site had no JavaScript. That was right
while the only dynamic thing was one waiting page.

## What Stayed the Same

- Pages are rendered on the server with `html/template`, from the same handlers, through
  `internal/app`. No single-page app, no JSON-to-DOM layer, and no second copy of the rules in
  JavaScript.
- Every page still works with JavaScript off. Forms keep their `action` and `method`, links stay
  links, and the live parts fall back to a refresh.
- No Node, no npm and no build step. `go build` still produces the whole thing.
- Nothing is loaded from a CDN. Everything is embedded and served by `andante-server`.

## Choices

### Interaction: htmx

[htmx](https://htmx.org/) lets an element fetch HTML from the server and swap it into the page,
driven by attributes. That fits a site whose handlers already render templates: htmx asks for the
part of a page it needs. Its SSE extension swaps each event's HTML into the page, so the live
progress needs no code of ours in the browser.

Considered:

- **Datastar.** Built around SSE, with client-side state as well. Younger and smaller than htmx, and
  we don't need the state.
- **Alpine.js.** Client-side state and behaviour we don't need, and its standard build uses
  `new Function`, which needs `unsafe-eval`.
- **React, Svelte, Vue.** A build step, a JSON API for the site to talk to, and the rules in two
  places.
- **Hand-written `fetch` and `EventSource` code.** Small to start with, and then it grows into a
  framework of our own.

### Live updates: Server-Sent Events

SSE is part of the HTML standard, goes over a plain HTTP response, sends the session cookie like
any other request, and reconnects by itself. WebSockets are two-way, which we don't need, and need a
library on the server. Polling every couple of seconds is the meta refresh with better manners.

### Styling: Pico CSS

[Pico CSS](https://picocss.com/) styles plain semantic HTML with few or no classes, which suits
templates that were already mostly plain HTML. It has light and dark built in, and everything it
draws comes from CSS custom properties, so a theme is a set of variables and not a second
stylesheet.

Considered: Tailwind (needs its CLI, and puts the styling in class lists in every template),
Bootstrap (heavier, and wants its classes everywhere) and Open Props (tokens only, so we'd still
write the components).

### Templates: `html/template`

[templ](https://templ.guide/) would give type-checked components, and is the usual pairing with
htmx in Go. With 17 templates, and `html/template` already able to render one named block as a
fragment, it wasn't worth another code generator. If the templates grow a lot, it's the first thing
to look at again.

### Themes: Design Tokens

A theme is a file in the [Design Tokens format](https://w3c.github.io/cg-reports/design-tokens/CG-FINAL-format-20251028/),
the W3C community group's JSON format, which Figma plugins, Style Dictionary and Terrazzo read and
write. It's the open standard for this, so a theme made in one of those tools is close to one
andante can take.

## Themes

The themes are files in [`internal/theme/themes/`](../../internal/theme/themes/), embedded in the
binary: `default` (Pico as it comes), `paper`, `slate` and `contrast`. The server reads them all
when it starts, and won't start if one doesn't parse. There's one theme for the whole site, which an
admin picks on the Users page, with `andante settings set --theme`, or as `theme` in
`PATCH /api/v1/settings`. `GET /api/v1/settings` lists the ones there are, with a description of
each. The choice is a column of the `settings` table.

A theme can set a small fixed set of tokens, each mapped to Pico's variables:

| Token | Type | Sets |
|---|---|---|
| `font.body` | `fontFamily` | `--pico-font-family` |
| `size.radius` | `dimension`, 0 to 4rem | `--pico-border-radius` |
| `size.spacing` | `dimension`, 0.25 to 4rem | `--pico-spacing` |
| `color.light.*`, `color.dark.*` | `color` | `background`, `surface`, `text`, `muted`, `border`, `primary`, `primary-hover` and `primary-inverse`, each to the Pico variables for it |

A token outside that set is refused, and so is a value that isn't its type: a colour has to be the
format's colour object in sRGB, or a hex string as earlier drafts had it, a dimension has to be px
or rem, and a font name can only have letters, digits, spaces and hyphens. The stylesheet is written
from the parsed numbers and names, never from the file's text, so a theme can't put CSS of its own
in. A colour set for one mode has to be set for the other, since otherwise light colours would sit
on Pico's dark ones. Pico's own `primary` variables have transparent variants (the underline, the
focus ring, the text selection), and andante makes those from the theme's `primary`.

A test checks every built-in theme for WCAG AA contrast (4.5:1) between text and background, muted
text and background, links and background, and button labels and buttons, in both modes.

The stylesheet is served at `/static/theme.css`, after Pico's and before `style.css`, so it's still
`style-src 'self'`. That's one URL whichever theme is chosen, with an ETag and `no-cache`, so the
browser asks again on each page and usually gets a 304. A change shows on the next page, on every
server sharing the database. If the database can't be read, or names a theme this server doesn't
have (after a downgrade, say), it serves the default, logs a warning and counts it in
`andante_web_theme_fallbacks_total`.

The feed page and its "next one now" confirmation use the same stylesheets, so a friend with a feed
link sees the same site. Their policy went from `default-src 'none'` to allowing `style-src 'self'`
and `img-src data:` (for Pico's icons), and still allows no scripts.

### No Library for the Tokens

The only Go library for the format is [asimonim](https://pkg.go.dev/bennypowers.dev/asimonim), a
parser, validator and language server from 2025 with one maintainer, built for editors (it keeps
line and column positions, and reads YAML). andante needs to read a dozen fixed tokens and refuse
everything else, which is a few hundred lines with `encoding/json` and does exactly the checks above.
If themes become uploadable and need aliases followed, it's worth looking at again.

## Where It Diverged

The divergences in phases 1 to 4 are in [`pages.md`](pages.md). In short: htmx 4 instead of 2, with
different config; `data:` images in the policy for Pico's icons; no live stream for the Read page;
the source state in words, without "caught up with readers"; and the catalogue's stream documented
with an `x-events` extension.

The themes diverged in these:

- **One URL for the theme.** The proposal had `/static/themes/{name}.css`. A fixed URL that follows
  the setting means the templates don't need to know the theme, and a change shows on every server
  at once, at the cost of a conditional request per page.
- **The settings list the themes.** `Settings` has `themes` as well as `theme`, so that a client
  can show the choice without knowing what's built in. `theme` is a string with a pattern, not an
  enum, since an enum that grows is a breaking change to a response.
- **Only sRGB, and no aliases.** The format allows other colour spaces and tokens that refer to
  other tokens (`{color.base}` or `$ref`). The built-in themes don't need either, and refusing them
  with a clear message is better than half supporting them.
- **The feed page is styled now.** It had no stylesheet at all before, which the proposal didn't
  mention.

What's left is in [`proposals/website.md`](../proposals/website.md): a live Read page, and themes an
admin uploads or each user picks.
