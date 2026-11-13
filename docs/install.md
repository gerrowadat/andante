# Installing

andante is two services: `andante-catalogue` fetches and stores serials, and `andante-server` does
everything else (the website, the API and the feeds). Each needs its own Postgres database. Both
binaries and the `andante` CLI are in one image, `ghcr.io/gerrowadat/andante`, and in the archives
on the [releases page](https://github.com/gerrowadat/andante/releases).

Use Docker Compose if you just want it working, or Nomad if you already have a cluster. Once it's
up, [bootstrapping](bootstrap.md) makes you the first admin, and [using andante](using.md) gets a
feed out of it. [Deploying](deploying.md) covers putting it on the internet and backups.

The Compose stack has been run for real, end to end. The Nomad job is an example to start from. It
validates, but it hasn't been run on a cluster.

## With Docker Compose

[`deploy/compose/`](../deploy/compose/) runs both services and a Postgres to keep them in.

```sh
cd deploy/compose
cp env.example .env
$EDITOR .env
docker compose up -d --wait
```

The `.env` needs five things:

| Variable | What it is |
|---|---|
| `POSTGRES_PASSWORD` | Anything long. `openssl rand -hex 32` will do |
| `CATALOGUE_DB_PASSWORD` | Same again, for the catalogue's database |
| `ANDANTE_SECRET` | Encrypts feed URLs so they can be shown again. Don't lose it |
| `ANDANTE_CATALOGUE_TOKEN` | What the server uses to talk to the catalogue |
| `ANDANTE_BASE_URL` | Where feed readers will reach this, because feed links are built from it |

If `:8080`, `:8081` or `:8082` is already taken, set `ANDANTE_PUBLIC_PORT`, `ANDANTE_OPS_PORT` or
`ANDANTE_CATALOGUE_PORT` too. The public port is open on all your interfaces. The other two are only
on loopback, and they should stay that way.

The optional settings (a proxy in front of it, logging in with an OpenID provider) go in an
`andante.env` next to `.env`. Copy it from `andante.env.example`.

Postgres only creates the catalogue's database on an empty volume. If you ran the v0.0.1 stack,
start again with `docker compose down -v`.

To run your own build instead of a release, `make docker` and set `ANDANTE_IMAGE=andante:dev`.

## With Nomad

You need a cluster and a Postgres (15 or newer) with a database for each service, because the job
doesn't bring its own. [`deploy/nomad/andante.nomad.hcl`](../deploy/nomad/andante.nomad.hcl) runs a task for each
service in one group, with `/readyz` checks. It takes its secrets from a Nomad Variable, so make
that first:

```sh
nomad var put nomad/jobs/andante \
  database_url='postgres://andante:...@db.example:5432/andante' \
  catalogue_database_url='postgres://catalogue:...@db.example:5432/catalogue' \
  secret="$(openssl rand -hex 32)" \
  catalogue_token="$(openssl rand -hex 32)"
```

Edit `datacenters`, both `ANDANTE_BASE_URL`s and the image tags in the job, then:

```sh
nomad job run deploy/nomad/andante.nomad.hcl
```

The job maps all its ports onto the host, so limit who can reach the ops and catalogue ports with
your cluster's firewall. Removing those mappings works too, but then Prometheus can't scrape them.
[Deploying](deploying.md#the-listeners) says what an open one gives away. Run one replica. A second one wouldn't crawl,
since the crawl loop takes a Postgres advisory lock.

The job sets `gitops_managed = "true"`, so [nomad-gitops](https://github.com/gerrowadat/nomad-gitops)
will watch it for drift if you run that.

## With Plain Docker

If you already have a Postgres, 15 or newer, bare containers will do, on a network they share:

```sh
docker network create andante
docker run -d --name catalogue --network andante \
  --entrypoint /usr/local/bin/andante-catalogue \
  -e ANDANTE_CATALOGUE_DATABASE_URL='postgres://.../catalogue' \
  -e ANDANTE_CATALOGUE_LISTEN=:8082 \
  -e ANDANTE_CATALOGUE_TOKEN="$TOKEN" \
  -e ANDANTE_BASE_URL=https://feeds.example.com \
  ghcr.io/gerrowadat/andante:0.1.1
docker run -d --name andante --network andante \
  -p 8080:8080 -p 127.0.0.1:8081:8081 \
  -e ANDANTE_DATABASE_URL='postgres://.../andante' \
  -e ANDANTE_BASE_URL=https://feeds.example.com \
  -e ANDANTE_OPS_LISTEN=:8081 \
  -e ANDANTE_SECRET="$SECRET" \
  -e ANDANTE_CATALOGUE_URL=http://catalogue:8082/v1 \
  -e ANDANTE_CATALOGUE_TOKEN="$TOKEN" \
  ghcr.io/gerrowadat/andante:0.1.1
```

Inside a container the ops listener has to bind to all interfaces to be published, so
`-p 127.0.0.1:` is the bit that keeps it private.

## From a Release Archive

Download the archive for your platform and `SHA256SUMS` from the
[releases page](https://github.com/gerrowadat/andante/releases), and
[verify them](releasing.md#verifying-an-artifact). Each archive has `andante-server`,
`andante-catalogue` and `andante`. Then, with a Postgres to hand:

```sh
export ANDANTE_BASE_URL=https://feeds.example.com ANDANTE_CATALOGUE_TOKEN="$TOKEN"
ANDANTE_CATALOGUE_DATABASE_URL='postgres://.../catalogue' ./andante-catalogue &
ANDANTE_DATABASE_URL='postgres://.../andante' ANDANTE_SECRET="$SECRET" \
  ANDANTE_CATALOGUE_URL=http://127.0.0.1:8082/v1 ./andante-server
```

Run them under whatever supervises services on that machine. Every setting is in
[configuration](configuration.md).

## From Source

You need Go (the version in `go.mod`) and Docker. This builds everything into `out/`, then starts
Postgres in Docker and both services with text logs:

```sh
make build
make run
```

The base URL is `http://localhost:8080`. The server's `--secret` is a fixed one from the Makefile,
which is fine for trying it out. Set `DEV_SECRET` to change it, and keep using the same one, because
the server won't start against a database set up with a different secret. [Development](development.md) has the rest of the make
targets.

## The CLI

The image has the CLI in it. Under Compose, run this from `deploy/compose/`:

```sh
alias andante='docker compose exec -T -e ANDANTE_TOKEN andante /usr/local/bin/andante'
```

Under Nomad:

```sh
andante() { nomad alloc exec -i -task server -job andante /usr/local/bin/andante --token "$ANDANTE_TOKEN" "$@"; }
```

`-task` goes before `-job`, or `nomad alloc exec` ignores it and asks which task you meant. In the
container the CLI finds the server from its `ANDANTE_LISTEN` and the path in its `ANDANTE_BASE_URL`,
so neither alias needs `--url`.

Or use the standalone one from a release archive, or `out/andante` from a source build, with
`ANDANTE_URL` set to the base URL. It needs an API token in `ANDANTE_TOKEN`, which you get once
you're [bootstrapped](bootstrap.md). See [the CLI](cli.md).

## Upgrading

Change the image tag (in `compose.yaml`, the Nomad job, or your `docker run`) and start it again.
Migrations run when each service starts, under a lock, so there's nothing to run by hand. Read
the [changelog](../CHANGELOG.md) first, because before 1.0 a release can need a fresh database.
There's no upgrade from v0.0.1.

## Checking It's Up

```sh
docker compose logs andante catalogue        # or: nomad alloc logs -job andante -task server -stderr
curl -s http://127.0.0.1:8081/readyz         # 200 means the server can reach its database
```
