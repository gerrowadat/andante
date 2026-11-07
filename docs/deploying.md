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
| Public | `:8080` | Feeds only | Feed readers, so the internet if you share feeds |
| Admin | `127.0.0.1:8081` | The API, `/healthz`, `/readyz`, `/metrics` | You and Prometheus, and nobody else |
| Catalogue | `127.0.0.1:8082` | Its private API, `/healthz`, `/readyz`, `/metrics` | `andante-server` and Prometheus |

Keep the admin listener on loopback or a private network. Its API needs a token, but
`/metrics` and the health endpoints don't.

Set `--base-url` to the address readers will use, because feed links are built from it.

On first start with no `--admin-token`, the server generates a token and logs it once.
Make others with `andante api-token`. [Configuration](configuration.md) has every setting.

## Nomad

See [running andante](running.md#with-nomad) for the job itself. One thing to be aware of:
the job maps both ports onto the host, so limit who can reach the admin port with your
cluster's firewall, or remove that mapping.

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
  -e ANDANTE_ADMIN_LISTEN=:8081 \
  -e ANDANTE_CATALOGUE_URL=http://catalogue:8082/v1 \
  -e ANDANTE_CATALOGUE_TOKEN="$TOKEN" \
  ghcr.io/gerrowadat/andante:0.0.1
```

Inside a container the admin listener has to bind to all interfaces to be published, so
`-p 127.0.0.1:` is the bit that keeps it private. The image holds the `andante` CLI too.

## Binary

Download an archive from the [releases page](https://github.com/gerrowadat/andante/releases),
[verify it](releasing.md#verifying-an-artifact), then:

```sh
export ANDANTE_BASE_URL=https://feeds.example.com ANDANTE_CATALOGUE_TOKEN="$TOKEN"
ANDANTE_CATALOGUE_DATABASE_URL='postgres://.../catalogue' ./andante-catalogue &
ANDANTE_DATABASE_URL='postgres://.../andante' ANDANTE_CATALOGUE_URL=http://127.0.0.1:8082/v1 ./andante-server
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

Then set `--base-url` to whatever name you chose. Don't route anything to the admin
listener. On a tailnet, reach it with `ANDANTE_URL=http://<host>:8081`.

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

Keep dumps private. Tokens are stored as hashes, but a dump still holds your reads and
history. Restored feeds keep their item IDs and positions, because a read's position is
computed from its stored history and the clock. Entries due while the server was down
appear with the times they were due. See [reads](reads.md).
