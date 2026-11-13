# Using andante

This assumes andante is [installed](install.md) and you've [bootstrapped](bootstrap.md) yourself an
account.

You add a **source** (the serial), make a **read** (a
paced replay of it), and issue a **token** (which gets you a feed URL).

You can do all of it on the website or with the CLI. The website is easier, and the CLI is below it
for scripting.

## On the Website

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
two-factor, your logged-in browsers and API tokens for the CLI. A password needs 8 characters, one
of them not a letter, unless an admin has said otherwise. The page asking for one says what it needs.

Two-factor takes a code from an authenticator app as well as your password. Setting it up gives you
ten recovery codes, each good for one login without your phone. An admin can require it for
everyone who logs in with a password, on the Users page or with `andante settings set
--require-two-factor`, and can turn off someone's if they've lost their phone and their codes.

The rest of this section does the same things with the CLI.

## Add a Serial

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

## Make a Read

A read needs a pace. Either a fixed schedule:

```sh
andante read create my-comic --daily 07:00 --tz Europe/Dublin
```

Or the original run's timing, sped up. This turns a weekly show into a daily one, and
stops a hiatus from stalling you for more than three days:

```sh
andante read create my-podcast --replay --speed 7 --max-gap P3D
```

## Subscribe

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

## Day to Day

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

## OPML

The CLI runs inside the container, so it can't see your files. Pass them on stdin:

```sh
andante import opml /dev/stdin < feeds.opml
andante export opml --issue viewer --label "For Alex" > feeds.opml
```

An import creates paused sources, so nothing crawls until you've looked at each one. Making a read on
one turns it on.

That's been tested under Compose, but I haven't tried it through `nomad alloc exec`. If it
doesn't behave, run the CLI from a machine that can reach the public listener instead. There's a
[standalone one](cli.md#install) in every release archive, and it takes `ANDANTE_URL` and
`ANDANTE_TOKEN`.

If something isn't working, see [installing](install.md#checking-its-up) and the [FAQ](faq.md).
