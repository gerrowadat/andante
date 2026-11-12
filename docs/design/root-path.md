# Design: Serving From a Path

Status: built. Asked for after v0.1.0, so it has no phase of its own.

andante can run at `https://example.com/feeds` as well as on a host of its own. The path comes from
`--base-url`, and both of the server's listeners serve under it, so health checks and metrics move
with the website, the API and the feeds.

## Decisions

- **No new flag.** `--base-url` already says where readers find andante, and feed links, invite
  links and the OpenID callback were already built from it. Prometheus does the same with
  `--web.external-url`. It also has `--web.route-prefix` for proxies that strip the path, and we
  could add one if anyone needs it. Nobody has asked.
- **The proxy passes the path through.** The handlers see paths with the root taken off
  (`rootpath.Mount`), so routing, the API's generated server and every test written for the top of
  a host stay as they are. A request outside the root is a 404, and the root itself redirects to
  the root with a slash.
- **Redirects are fixed in one place.** About thirty handlers redirect to paths like `/reads/3`.
  Rather than touch each, `Mount` adds the root to any `Location` that starts with a single slash.
  It doesn't change a redirect's body, which nobody reads.
- **Pages say `{{root}}`.** Templates can't be fixed from outside, so every link to one of our own
  pages starts with `{{root}}`. The templates are parsed once with an empty root and each site
  runs its own copy (`rooted` in `internal/web/pages.go`), because a template can't take new
  functions once it has run.
- **The session cookie is `__Secure-` under a path.** `__Host-` needs `Path=/`. Paths on one host
  share an origin anyway, so the cookie's path is tidiness rather than a boundary, and
  [deploying](../deploying.md#under-a-path) says so.
- **Roots are plain.** Only letters, digits and `. _ ~ -` between slashes, so a root never needs
  escaping in a URL, a cookie or a template.
- **The catalogue doesn't move.** Only the server talks to it, so it has no public URL to match.
  Its `--base-url` is just named in its `User-Agent`.
