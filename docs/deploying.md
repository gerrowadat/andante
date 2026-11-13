# Deploying

andante is two services and a Postgres with a database for each. [Installing](install.md) gets it
running with Docker Compose, Nomad, plain Docker or a binary. This page is the rest of it: what to
expose, and backups.

Postgres has to exist already, unless you're using the Compose stack, and has to be 15 or newer.
CI runs the tests against 15 and 18. That's the oldest version Postgres itself still supports, and
the migrations don't need anything newer. Migrations run on start, under a lock, so there's nothing
to run by hand. If one fails, the service logs Postgres's error and exits, and on a server that's
too old that error won't mention the version.

## The Listeners

| Listener | Default | Carries | Who should reach it |
|---|---|---|---|
| Public | `:8080` | The website, feeds and the API | Browsers, feed readers and the CLI |
| Ops | `127.0.0.1:8081` | `/healthz`, `/readyz`, `/metrics` | Prometheus, and nobody else |
| Catalogue | `127.0.0.1:8082` | Its private API, `/healthz`, `/readyz`, `/metrics` | `andante-server` and Prometheus |

`/healthz`, `/readyz` and `/metrics` have no auth, on either service. The health checks say `ok` or
`not ready` and nothing else. The metrics ([monitoring](monitoring.md) lists them) carry the running
version and commit, each source's slug and state, counts of requests, crawls, feed fetches and
logins, and Go's runtime and process stats. They never carry tokens, read IDs, usernames or URLs.
So someone who can reach them learns which serials you've added, roughly how busy the instance is
and which version to look up advisories for. That's fine on a private network. I wouldn't put it
on the internet.

The rest of the catalogue's listener is its API, and every call needs `--api-token`. That token
goes over plain HTTP, though, so keep the catalogue on loopback or a network you trust, for the
token's sake more than the metrics'.

Where you can't use loopback, such as Nomad with host networking, bind these to an address that
only the server and Prometheus can reach, or firewall the ports to them.

The public listener is fine on the internet, behind a proxy that does TLS. Set
`--trusted-proxies` to the proxy's address, or every login looks like it came from the proxy, and
throttling treats them all as one address. Under Compose, that and the OpenID settings go in an
`andante.env` next to `.env`. Copy it from `andante.env.example`.

Each login can take 64 MiB, and two run at once, so leave room for that. The Nomad job allows for it.

Set `--base-url` to the address readers will use, because feed links are built from it.

A new database has no admin. [Bootstrapping](bootstrap.md) covers making one.
[Configuration](configuration.md) has every setting.

## Under a Path

To run andante at `https://example.com/feeds` instead of on a host of its own, put the path in
`--base-url`. Everything the server serves moves under it, on both listeners: the website, the API,
feeds, and `/feeds/healthz`, `/feeds/readyz` and `/feeds/metrics`. Anything outside it is a 404.
Point Prometheus and any health checks at the new paths, and the CLI's `ANDANTE_URL` at
`https://example.com/feeds`.

The proxy has to pass the path through as it is, without taking `/feeds` off. In nginx that's a
`proxy_pass` with no path of its own:

```nginx
location /feeds/ {
    proxy_pass http://127.0.0.1:8080;
}
```

In Caddy, use `handle /feeds/*`, because `handle_path` strips it. The catalogue doesn't move, since
nothing but the server talks to it.

Browsers don't separate paths on one host. Anything else served from `example.com` is the same
origin as andante, and a script there can use the website as whoever is logged in. Only put andante
under a path of a host whose other content you trust. The session cookie is also `__Secure-` and not
`__Host-` there, because a `__Host-` cookie can only have a path of `/`.

## Exposing Only the Feeds

Publish the public listener and nothing else. Either of these works without opening a port
on your router.

With [Tailscale Funnel](https://tailscale.com/kb/1223/funnel):

```sh
tailscale funnel --bg 8080
```

With a [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/),
route a hostname to the public listener in the tunnel's config:

```yaml
ingress:
  - hostname: feeds.example.com
    service: http://localhost:8080
  - service: http_status:404
```

Then set `--base-url` to whatever name you chose. Don't route anything to the ops or catalogue
listeners. That publishes the website and the API as well, which is what you want if anyone else logs in.

## Backups

Everything lives in Postgres, so a backup is a `pg_dump` of each database. The server's matters
most, because the catalogue can be crawled again and people's reads can't. It also holds when each
entry first arrived, which release times depend on:

```sh
pg_dump --format=custom --file=andante.dump "$ANDANTE_DATABASE_URL"
pg_restore --clean --if-exists --dbname="$ANDANTE_DATABASE_URL" andante.dump
```

Under the Compose stack, go through the database container:

```sh
docker compose exec -T postgres pg_dump --format=custom -U andante andante > andante.dump
docker compose exec -T postgres pg_dump --format=custom -U andante catalogue > catalogue.dump
docker compose exec -T postgres pg_restore --clean --if-exists -U andante -d andante < andante.dump
docker compose exec -T postgres pg_restore --clean --if-exists -U andante -d catalogue < catalogue.dump
```

Keep dumps private, and store `--secret` separately. Feed tokens in a dump are sealed with it, and
with both, someone has your feed URLs. A dump without the secret still holds your reads and history. Restored feeds keep their item IDs and positions, because a read's position is
computed from its stored history and the clock. Entries due while the server was down
appear with the times they were due. See [reads](reads.md).
