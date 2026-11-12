# andante

[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/gerrowadat/andante/badge)](https://scorecard.dev/viewer/?uri=github.com/gerrowadat/andante)

Read, watch or listen to an archive at the pace of its original run.

andante takes a serial with a long back catalogue (a webcomic, a podcast, a blog, a news
site) and releases it to you gradually instead of all at once. Pick a fixed pace (one a
day, three on weekdays, ten on Saturdays) or replay the original run's timing, sped up if
you like (a weekly comic as a daily one).

You get a normal feed (Atom, RSS 2.0 or JSON Feed) for whatever feed reader or podcast app
you already use. Feeds are shareable, so a few friends can read or listen along
together. Going back to the start is a new read.

> Status: alpha. v0.1.0 is the first release with accounts and a website. Expect rough edges.

## Quick Start

The real instructions are in [installing](docs/install.md), with Docker Compose, Nomad or a
binary. The short version, from the image:

```sh
cd deploy/compose
cp env.example .env && $EDITOR .env
docker compose up -d --wait
docker compose logs andante | grep bootstrap
```

That last line is the address of the setup page and a token for it. Open it, pick a name and a
password, and you're the admin. [Bootstrapping](docs/bootstrap.md) has the details, and how to let
other people in.

To run from source instead, you need Go and Docker. `make build && make run` starts Postgres and
both services on `http://localhost:8080`, and logs the setup page the same way.

Then, on the website, **Add** a serial from its address, **Track this** at a pace, and paste the
feed link into your feed reader or podcast app. The same from the CLI, with an API token from the
Account page:

```sh
andante source discover https://pod.example/     # what it found, saving nothing
andante source add https://pod.example/rss --strategy feed --slug my-podcast --title 'My Podcast' \
  --home-url https://pod.example/ --kind episode
andante read create my-podcast --replay --speed 7
andante token add 1 --label me --owner
```

The feed URL is built from the base URL, so a reader on another machine needs a real address. See
[deploying](docs/deploying.md).

## How It Works

1. **Discovery**, in its own service, builds an ordered index of the serial's entries and stores it in
   Postgres. Where the source's feed holds the whole archive (most podcasts, some blogs)
   that's enough. Otherwise andante crawls the site page by page (most webcomics). It keeps
   to robots.txt, rate limits and conditional requests, and it
   indexes lazily instead of mirroring archives up front.
2. **Release** works out from your pacing (an iCalendar RRULE, or the original run's
   gaps) how far through the index you've got by now.
3. **Serving** renders the released entries as a feed. Images, audio and video are linked
   from the original site and never copied. Feed requests never trigger a crawl.

## Documentation

- [Installing](docs/install.md): Docker Compose, Nomad, a binary or from source
- [Bootstrapping](docs/bootstrap.md): the first admin, and letting people in
- [Using andante](docs/using.md): adding a serial, pacing it, subscribing
- [Deploying](docs/deploying.md): what to expose, and backups
- [Design philosophy](docs/philosophy.md)
- [FAQ](docs/faq.md)
- [Documentation index](docs/README.md)

## License

Apache-2.0. See [LICENSE](LICENSE).
