# Running It

andante is two services: `andante-catalogue` fetches and stores serials, and `andante-server` does
everything else. Both are in one image. This gets andante running from that image, points it at a serial, and gets a paced
feed out the other end. Use Docker Compose if you just want it working, or Nomad if you already
have a cluster.

[Deploying](deploying.md) covers exposing ports, tunnels and backups.

The Compose stack below has been run for real, end to end. The Nomad job is an example to start from.
It validates, but it hasn't been run on a cluster. Patches welcome.

To run your own build, use `make docker` and set `ANDANTE_IMAGE=andante:dev` (Compose), or point
the job's `image` at your own registry (Nomad).

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

Postgres only creates the catalogue's database on an empty volume. If you ran an older version of
this stack, start again with `docker compose down -v`.

There are no users yet, so add yourself. This prints your API token:

```sh
docker compose exec -T andante /usr/local/bin/andante-server user add dave --admin
```

The image has the CLI in it, so with that token an alias gets you going:

```sh
export ANDANTE_TOKEN=<your token>
alias andante='docker compose exec -T -e ANDANTE_TOKEN andante /usr/local/bin/andante'
andante whoami
```

Run that from `deploy/compose/`. To upgrade, change the tag in `compose.yaml` and run
`docker compose up -d` again. Migrations run when the server starts.

## With Nomad

You need a cluster and a Postgres with a database for each service, because the job doesn't bring
its own. [`deploy/nomad/andante.nomad.hcl`](../deploy/nomad/andante.nomad.hcl) runs a task for each
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

Then add yourself, which prints your API token. The CLI is in the image here too:

```sh
nomad alloc exec -job andante -task server /usr/local/bin/andante-server user add dave --admin
export ANDANTE_TOKEN=<your token>
andante() { nomad alloc exec -job andante -task server /usr/local/bin/andante --token "$ANDANTE_TOKEN" "$@"; }
andante whoami
```

The job sets `gitops_managed = "true"`, so [nomad-gitops](https://github.com/gerrowadat/nomad-gitops)
will watch it for drift if you run that. Run one replica. A second one wouldn't crawl, since the
crawl loop takes a Postgres advisory lock.

## Using It

The rest is the same either way. You add a **source** (the serial), make a **read** (a
paced replay of it), and issue a **token** (which gets you a feed URL).

You can do all of it on the website or with the CLI. The website is easier, and the CLI is below it
for scripting.

### Log In

To set a password for the first admin:

```sh
andante-server user link dave --base-url https://feeds.example.com
```

Run that the same way as `user add`, and open the link it prints. To invite someone else:

```sh
andante invite add alex
```

The link works once and lasts a week. With an `http://` base URL the session cookie isn't `Secure`, which
is only good enough for trying it out. An admin can also invite people from the Users page.

With an OpenID provider set up, the login and invite pages have single sign-on as well. See
[configuration](configuration.md#logging-in-with-a-provider).

### On the Website

Open the base URL and log in.

1. **Browse** lists what's already here, because someone may have added the serial you want. If
   they haven't, **Add** takes the site's address, works out how to read it and shows you the first
   few entries. If it can't, it asks for the address of the very first page.
2. **Track this**, on a source's page, asks for a pace (one a day, weekdays, chosen days, the
   original timing sped up, or an RRULE) and makes a read.
3. The read's page has your feed link. Paste it into your feed reader or podcast app. Add a link for
   each friend reading along, so that you can stop one without stopping the rest. They don't need an
   account, and opening the link in a browser shows them what it is and how fast it goes.

Pause, skip, jump and change the pace from the same page. The Account page has your password,
two-factor, your logged-in browsers and API tokens for the CLI.

Two-factor takes a code from an authenticator app as well as your password. Setting it up gives you
ten recovery codes, each good for one login without your phone. An admin can require it for
everyone who logs in with a password, on the Users page or with `andante settings set
--require-two-factor`, and can turn off someone's if they've lost their phone and their codes.

The rest of this section does the same things with the CLI.

### Add a Serial

Start with the site's address. This works out how to read it, tries what it finds, and tells you
if someone has already added it:

```sh
andante source discover https://comic.example/
```

If it comes up empty, or you'd rather say how yourself, a probe tries a config and saves nothing.

For a podcast, or anything else that keeps its whole archive in its feed:

```sh
andante source probe https://pod.example/rss --strategy feed --kind episode
andante source add https://pod.example/rss --strategy feed --slug my-podcast \
  --title 'My Podcast' --home-url https://pod.example/ --kind episode
```

For a webcomic, which almost certainly doesn't:

```sh
andante source probe https://comic.example/1
andante source add https://comic.example/1 --slug my-comic --title 'My Comic'
```

Crawling keeps to one request every 5 seconds per host by default, so a probe of 10 pages
takes about 45 seconds. See [sources](sources.md) for picking a strategy, and for what to do
when a probe comes back with warnings.

Check how it's getting on:

```sh
andante source list
andante source entries my-podcast
```

`tail` means it has reached the live end and is now only looking for new entries.

### Make a Read

A read needs a pace. Either a fixed schedule:

```sh
andante read create my-comic --daily 07:00 --tz Europe/Dublin
```

Or the original run's timing, sped up. This turns a weekly show into a daily one, and
stops a hiatus from stalling you for more than three days:

```sh
andante read create my-podcast --replay --speed 7 --max-gap P3D
```

### Subscribe

Issue yourself a token, using the read's ID:

```sh
andante read list
andante token add 1 --label me --owner
```

This prints the Atom, RSS and JSON Feed URLs. Paste one into your feed reader or podcast
app. `andante token list 1` shows them again. `--owner` adds a "next one now" link to each item.

To read along with friends, give each of them their own viewer token, with no account needed, so that you can
revoke one without breaking the rest:

```sh
andante token add 1 --label Alex
```

### Day to Day

```sh
andante read pause 1
andante read resume 1
andante read skip 1              # one more now
andante read pace 1 --daily 18:00
andante token list 1             # who's still reading?
andante token rm 3
```

Nothing here rewrites history, so items your reader already has stay put. To start a
serial again, make a new read. See [reads](reads.md) and [feeds](feeds.md), and
[the CLI docs](cli.md) for everything else.

### OPML

The CLI runs inside the container, so it can't see your files. Pass them on stdin:

```sh
andante import opml /dev/stdin < feeds.opml
andante export opml --issue viewer --label "For Alex" > feeds.opml
```

An import creates paused sources, so nothing crawls until you've looked at each one. Making a read on
one turns it on.

That's been tested under Compose, but I haven't tried it through `nomad alloc exec`. If it
doesn't behave, run the CLI from a machine that can reach the admin port instead. There's a
[standalone one](cli.md#install) in every release archive, and it takes `ANDANTE_URL` and
`ANDANTE_TOKEN`.

## If Something's Wrong

```sh
docker compose logs andante catalogue        # or: nomad alloc logs -job andante -task server -stderr
curl -s http://127.0.0.1:8081/readyz         # 200 means it can reach its database. Ops listener
```

See the [FAQ](faq.md) for the common ones.
