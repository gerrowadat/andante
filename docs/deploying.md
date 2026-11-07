# Deploying

andante is two services and a Postgres with a database for each. If you just want it running, go to
[running andante](running.md), which walks through Docker Compose and Nomad. This page is
the rest of it.

Postgres has to exist already, unless you're using the Compose stack, and any recent
version will do (CI uses 18). Migrations run on start, under a lock, so there's nothing to
run by hand.

## The Listeners

| Listener | Default | Carries | Who should reach it |
|---|---|---|---|
| Public | `:8080` | Feeds and the API | Feed readers and the CLI |
| Ops | `127.0.0.1:8081` | `/healthz`, `/readyz`, `/metrics` | Prometheus, and nobody else |
| Catalogue | `127.0.0.1:8082` | Its private API, `/healthz`, `/readyz`, `/metrics` | `andante-server` and Prometheus |

Keep the ops listener on loopback or a private network, because `/metrics` and the health endpoints
have no auth. Logins are throttled, but there's no two-factor or OIDC yet, so hold off on putting this
on the open internet. Behind a proxy, set `--trusted-proxies`, or every login looks like it came from the
proxy, and throttling treats them all as one address.

Each login can take 64 MiB, and two run at once, so leave room for that. The Nomad job allows for it.

Set `--base-url` to the address readers will use, because feed links are built from it.

A new database has no users, so run `andante-server user add <name> --admin` for your first API token.
[Configuration](configuration.md) has that and every other setting.

## Nomad

See [running andante](running.md#with-nomad) for the job itself. One thing to be aware of:
the job maps all its ports onto the host, so limit who can reach the ops and catalogue ports with
your cluster's firewall, or remove those mappings.

## Docker

Compose is covered in [running andante](running.md#with-docker-compose). If you already have a
Postgres, bare containers will do, on a network they share:

```sh
docker network create andante
docker run -d --name catalogue --network andante \
  --entrypoint /usr/local/bin/andante-catalogue \
  -e ANDANTE_CATALOGUE_DATABASE_URL='postgres://.../catalogue' \
  -e ANDANTE_CATALOGUE_LISTEN=:8082 \
  -e ANDANTE_CATALOGUE_TOKEN="$TOKEN" \
  -e ANDANTE_BASE_URL=https://feeds.example.com \
  ghcr.io/gerrowadat/andante:0.0.1
docker run -d --name andante --network andante \
  -p 8080:8080 -p 127.0.0.1:8081:8081 \
  -e ANDANTE_DATABASE_URL='postgres://.../andante' \
  -e ANDANTE_BASE_URL=https://feeds.example.com \
  -e ANDANTE_OPS_LISTEN=:8081 \
  -e ANDANTE_SECRET="$SECRET" \
  -e ANDANTE_CATALOGUE_URL=http://catalogue:8082/v1 \
  -e ANDANTE_CATALOGUE_TOKEN="$TOKEN" \
  ghcr.io/gerrowadat/andante:0.0.1
```

Inside a container the ops listener has to bind to all interfaces to be published, so
`-p 127.0.0.1:` is the bit that keeps it private. The image holds the `andante` CLI too.

## Binary

Download an archive from the [releases page](https://github.com/gerrowadat/andante/releases),
[verify it](releasing.md#verifying-an-artifact), then:

```sh
export ANDANTE_BASE_URL=https://feeds.example.com ANDANTE_CATALOGUE_TOKEN="$TOKEN"
ANDANTE_CATALOGUE_DATABASE_URL='postgres://.../catalogue' ./andante-catalogue &
ANDANTE_DATABASE_URL='postgres://.../andante' ANDANTE_SECRET="$SECRET" ANDANTE_CATALOGUE_URL=http://127.0.0.1:8082/v1 ./andante-server
```

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
listeners. Note that this publishes the API too.

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
