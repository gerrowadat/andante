# andante

[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/gerrowadat/andante/badge)](https://scorecard.dev/viewer/?uri=github.com/gerrowadat/andante)

Read, watch or listen to an archive at the pace of its original run.

andante takes a serial with a long back catalogue — a webcomic, a podcast, a
blog, a news site — and releases it to you gradually instead of all at once.
Choose a fixed pace (one a day, three on weekdays, ten on Saturdays) or replay
the original run's timing, sped up if you like (a weekly comic as a daily one).
You get a normal feed — Atom, RSS 2.0 or JSON Feed — for any feed reader or
podcast app. Feeds are shareable, so a group of friends can read or listen
along together. Going back to the start is a new read.

> Status: alpha. The features are built and tested, and v0.1.0 is the first release.
> Expect rough edges.

## Quick start

You need Go and Docker. This starts Postgres and the server, which logs a generated
API token once:

```sh
make build
make run
```

In another terminal, add a serial and subscribe to it:

```sh
export ANDANTE_TOKEN=<the token from the log>
out/andante source probe https://pod.example/rss --strategy feed --kind episode
out/andante source add https://pod.example/rss --strategy feed --slug my-podcast --title 'My Podcast' \
  --home-url https://pod.example/ --kind episode
out/andante read create my-podcast --replay --speed 7
out/andante token add 1 --label me --owner
```

Paste the feed URL into your feed reader or podcast app. The URL uses `--base-url`, so
a reader on another machine needs a real address. See [deploying](docs/deploying.md).

## How it works

1. **Discovery** builds an ordered index of the serial's entries and stores it
   in Postgres. Where the source's feed holds the whole archive (most podcasts,
   some blogs) that is enough; otherwise andante crawls the site page by page
   (most webcomics). It is polite (robots.txt, rate limits, conditional
   requests) and indexes lazily rather than mirroring archives up front.
2. **Release** works out from your pacing — an iCalendar RRULE, or the
   original run's gaps — how far through the index you have got by now.
3. **Serving** renders the released entries as a feed. Images, audio and video
   are linked from the original site, never copied. Feed requests never
   trigger a crawl.

## Documentation

- [Design philosophy](docs/philosophy.md)
- [Documentation index](docs/README.md)
- [Build plan](docs/plan/README.md)
- [Deploying](docs/deploying.md)
- [FAQ](docs/faq.md)

## License

Apache-2.0. See [LICENSE](LICENSE).
