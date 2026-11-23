# User Guide

This is for people who use an andante instance someone else runs. You don't need to know how it
works inside, and there's nothing here about installing it. If you're the person running it, start
with [installing](install.md) and [bootstrapping](bootstrap.md), and send everyone else here.

## What andante Does

Say you've found a webcomic with ten years of archive, or a podcast with three hundred episodes.
You could binge it, or you could read it the way the people who were there at the start did: a bit
at a time, turning up in your feed reader alongside everything else.

andante does the second one. Give it the site's address, pick a pace, and it gives you a feed that
starts at the beginning and releases one entry at a time on your schedule. One a day before work.
Weekdays only. Or the original run's own timing sped up, so that a weekly comic comes out daily,
and a two-year hiatus takes a couple of days.

It works with anything that comes out in instalments:

- **Webcomics**, which almost never keep their archive in a feed. andante reads the site page by
  page instead.
- **Podcasts**. The feed works in any podcast app, and the audio comes from the publisher as it
  always did.
- **Blogs and serial fiction**, read from their feeds or their pages.

What you get is an ordinary feed (Atom, RSS or JSON Feed), so it works in whatever you already use.
There's no app to install and nothing to log into on your phone.

## Getting In

Someone who runs andante, or who already uses it, sends you an invite link. Open it and choose a
password.

![The invite page, asking Sam to choose a password](images/guide/invite.png)

After that, log in at the site's address with your username and password. If the site uses single
sign-on, there's a button for that instead.

![The login page](images/guide/login.png)

You start with nothing.

![Your reads, empty, with links to browse or add a site](images/guide/home-empty.png)

## Finding Something to Read

**Browse** lists every serial anyone on this andante instance has added. Serials are shared, so if
someone got there first you can start straight away.

![Browse, listing Gannet Point and Tide Tables](images/guide/browse.png)

If it isn't there, **Add** takes the site's address. Use the front page, or the feed's address for a
podcast.

![The Add page with a webcomic's address in it](images/guide/add.png)

andante works out how to read the site, which takes a minute or two because it only asks a site for
one page every few seconds. It shows you what it found and the first few entries, so you can check
it started in the right place.

![What andante found: Gannet Point, read page by page, starting at The New Keeper](images/guide/found.png)

If those aren't the first entries, or it found nothing, give it the address of the very first page
under **Not right?**. It walks forward from there by each page's "next" link.

Each serial has a page showing how far andante has got through its archive. A big archive keeps
filling in after you've started, and andante keeps checking for new entries once it reaches the end.

![Gannet Point's page: 24 entries, and a Track this button](images/guide/source.png)

## Reads

A **read** is your copy of a serial: where you've got to and how fast you're going. **Track this**
on a serial's page makes one. Give it a name and a pace.

![Tracking Gannet Point every weekday at 07:30](images/guide/track.png)

The paces are:

| Pace | What it does |
|---|---|
| One a day | One entry every day, at a time you choose. |
| Weekdays | The same, Monday to Friday. |
| On these days | The days you list, such as `MO,WE,FR`. |
| The original timing | The gaps the serial first came out with, divided by the speed. 7 turns a weekly serial into a daily one. Set "at most this many days between entries" and a long break in the original won't stall you. |
| An RRULE | Anything a calendar repeat rule can say, for when the others don't fit. |

"How many at a time" releases more than one entry at each time, for a serial with short entries.

Here's a weekly podcast at 14 times the speed, so it comes out daily, and its gaps between seasons
take two days at most:

![Tracking Tide Tables at the original timing, 14 times as fast, at most 2 days between entries](images/guide/track-replay.png)

You can have as many reads as you like, including more than one of the same serial. Nobody else can
see your reads.

![Your reads: Gannet Point on weekdays, and Tide Tables at the original timing](images/guide/home.png)

### Your Read's Page

The read's page says how far along you are and when the next entry comes out. It keeps itself up
to date, so you can leave it open.

![The read's page, with the pace, what's been released, the controls and the feed links](images/guide/read.png)

From here you can:

- **Pause** it while you're on holiday, and **Resume** when you're back. Nothing piles up while it's
  paused.
- **Release the next one now**, when you can't wait until tomorrow.
- **Jump** ahead to an entry, if you've read some of it already. Entries are numbered from 1, as in
  the source page's list, so jumping to entry 5 releases the first five.
- **Change the pace**. The new pace starts from where you are.

![Changing the pace of a read](images/guide/repace.png)

When you catch up with the serial, new entries come out as the publisher posts them.

None of these take back what's already in your feed reader. To start a serial again from the
beginning, make a new read.

## Subscribing in a Feed Reader

Every read comes with a feed link for you, labelled "me". Open it, and you get a page with the
feeds on it.

![The feed page, with Atom, RSS and JSON Feed links to copy](images/guide/feed-page.png)

Feed readers that do autodiscovery (Miniflux, NetNewsWire, FreshRSS and most others) will take
the page's own link and find the feeds from it. If yours doesn't, or you want a particular
format, copy one of these instead:

- **Atom** for most feed readers (Miniflux, NetNewsWire, Feedly, Inoreader, FreshRSS and the like).
- **RSS** for podcast apps (Pocket Casts, Overcast, AntennaPod, Apple Podcasts).
- **JSON Feed** if your reader prefers it.

They all have the same entries. Here's Miniflux:

![Adding the Atom link in Miniflux](images/guide/reader-subscribe.png)

Whatever's been released shows up straight away, and the rest arrives on schedule.

![Gannet Point's first six strips in Miniflux](images/guide/reader-entries.png)

Each entry has the comic or the episode, when it was first published, and a link to the original
page. Your own link also adds a "next one now" link at the bottom, which does the same as the
button on the read's page, so you can keep going from your feed reader.

![One strip in Miniflux, with Originally published and next one now links under it](images/guide/reader-entry.png)

Feed links work without logging in, which is how feed readers need them, so treat them like
passwords. Anyone who has one can read along.

## Reading Along with Friends

Add a link for each person under **Feed links** on the read's page. They don't need an account, and
they all get the same entries at the same time. Opening the link in a browser shows them the feed
page above, which says what it is and how fast it goes.

Tick **Can release the next one** only for people you'd trust with the pace, because that link can
move everyone on.

**Last seen** shows when each link was last used, so you can tell who's still reading. **Stop**
turns one link off and leaves everyone else alone.

## Your Account

Your name in the menu goes to your account page. From there you can:

- change your password,
- turn on two-factor, with any authenticator app (it gives you recovery codes for when you lose
  your phone),
- log out browsers you've left logged in,
- pick a colour theme (each has light and dark, following your device), and
- make API tokens, if you want to use the [command line](cli.md).

If you can invite people, **Invites** in the menu makes links for them.

## When Something Looks Wrong

- **It found the wrong first page, or nothing at all.** Use **Not right?** on the Add page with the
  address of the very first page.
- **A new entry hasn't turned up.** Check the read's page for when the next one is due. Your feed
  reader also only checks every so often, usually every half hour or more.
- **My reader says a strip from 2012 came out today.** That's on purpose. Today is when it came out
  to you, and a reader would bury an entry dated 2012. The original date is in the entry itself. The
  [FAQ](faq.md) has more on this and other surprises.

For anything else, ask whoever runs your andante instance.
