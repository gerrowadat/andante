# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Dave O'Connor
#
# Writes the two made-up serials the user guide's screenshots use: a webcomic,
# Gannet Point, with no feed, and a podcast, Tide Tables, whose
# feed has everything. Run by scripts/guide/screenshots.sh.

import datetime
import pathlib
import sys

COMIC_HOST = "http://gannetpoint.example"
PODCAST_HOST = "http://tidetables.example"

STRIPS = [
    "The New Keeper", "First Night", "The Logbook", "Fog", "A Gannet", "Supply Boat",
    "The Lamp Room", "Gale Warning", "Gale", "After the Gale", "A Letter", "The Reply",
    "Mackerel", "Visitors", "The Foghorn", "Low Tide", "The Causeway", "Stranded",
    "Morning", "The Inspector", "Whitewash", "A Second Gannet", "Equinox", "Winter Stores",
]

EPISODES = [
    ("Why the Tide Comes In Twice", "The moon, the sun, and the bulge on the far side of the planet."),
    ("Spring Tides Aren't About Spring", "Where the name comes from, and why it confuses everyone."),
    ("Tide Tables Before Computers", "Brass machines with pulleys that predicted a year in an afternoon."),
    ("The Bay of Fundy", "Sixteen metres, twice a day."),
    ("Places With One Tide a Day", "Diurnal tides, and why the Gulf of Mexico is odd."),
    ("Amphidromic Points", "Spots in the sea where the tide doesn't rise at all."),
    ("Tidal Bores", "Surfing a river upstream."),
    ("Reading a Tide Table", "Datum, heights, and the rule of twelfths."),
    ("Causeways", "Islands you can walk to, some of the time."),
    ("Tides and Harbours", "Why old ports are where they are."),
    ("King Tides", "Perigee, and the highest water of the year."),
    ("Listener Questions", "Mostly about the moon."),
]

SKIES = [("#f6d365", "#fda085"), ("#a1c4fd", "#c2e9fb"), ("#2c3e50", "#4ca1af"), ("#d7d2cc", "#304352"),
         ("#89f7fe", "#66a6ff"), ("#fbc2eb", "#a6c1ee")]


def lighthouse(x, night):
    beam = (f'<polygon points="{x + 22},38 {x + 150},10 {x + 150},70" fill="#fff6a8" opacity="0.6"/>'
            if night else "")
    return (f'{beam}<polygon points="{x},190 {x + 44},190 {x + 36},50 {x + 8},50" fill="#f4f1ea"/>'
            f'<rect x="{x + 8}" y="88" width="28" height="14" fill="#c0392b"/>'
            f'<rect x="{x + 4}" y="140" width="36" height="14" fill="#c0392b"/>'
            f'<rect x="{x + 10}" y="30" width="24" height="20" fill="{"#fff6a8" if night else "#9fb7c9"}"/>'
            f'<polygon points="{x + 6},30 {x + 38},30 {x + 22},16" fill="#2d3436"/>')


def keeper(x):
    return (f'<circle cx="{x}" cy="160" r="9" fill="#f0c8a0"/>'
            f'<rect x="{x - 9}" y="169" width="18" height="24" rx="4" fill="#34495e"/>'
            f'<rect x="{x - 11}" y="148" width="22" height="6" fill="#2d3436"/>')


def gannet(x, y):
    return f'<path d="M{x},{y} q12,-10 24,0 q12,-10 24,0" stroke="#2d3436" stroke-width="3" fill="none"/>'


def strip(n, title):
    top, bottom = SKIES[n % len(SKIES)]
    night = n % 4 == 1
    panels = []
    for p in range(3):
        x0 = p * 300
        body = [f'<rect x="{x0 + 6}" y="6" width="288" height="288" fill="url(#sky{n})" rx="6"/>',
                f'<rect x="{x0 + 6}" y="190" width="288" height="104" fill="#1f5f8b" rx="6"/>',
                f'<path d="M{x0 + 6},200 q36,-10 72,0 t72,0 t72,0 t72,0" stroke="#7fb3d5" stroke-width="3" fill="none"/>']
        if p == 0:
            body.append(lighthouse(x0 + 40, night))
            body.append(keeper(x0 + 200))
        elif p == 1:
            body.append(keeper(x0 + 120))
            body.append(gannet(x0 + 170 + n % 5 * 8, 70))
            body.append(f'<rect x="{x0 + 40}" y="40" width="170" height="44" rx="10" fill="#fff" stroke="#2d3436"/>'
                        f'<text x="{x0 + 125}" y="68" font-family="sans-serif" font-size="16" text-anchor="middle">{title}</text>')
        else:
            body.append(lighthouse(x0 + 200, night))
            body.append(gannet(x0 + 60, 50 + n % 3 * 20))
            body.append(gannet(x0 + 110, 90))
        panels.append(f'<g>{"".join(body)}<rect x="{x0 + 6}" y="6" width="288" height="288" rx="6" '
                      f'fill="none" stroke="#2d3436" stroke-width="4"/></g>')
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="900" height="300" viewBox="0 0 900 300">'
            f'<defs><linearGradient id="sky{n}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="{top}"/>'
            f'<stop offset="1" stop-color="{bottom}"/></linearGradient></defs>'
            f'<rect width="900" height="300" fill="#fff"/>{"".join(panels)}</svg>')


def comic(out):
    out.mkdir(parents=True, exist_ok=True)
    start = datetime.date(2012, 4, 2)
    last = len(STRIPS)
    for i, title in enumerate(STRIPS, 1):
        date = start + datetime.timedelta(weeks=i - 1)
        (out / f"{i}.svg").write_text(strip(i, title))
        nav = [f'<a class="cc-first" href="/1.html">First</a>']
        if i > 1:
            nav.append(f'<a class="cc-prev" rel="prev" href="/{i - 1}.html">Back</a>')
        if i < last:
            nav.append(f'<a class="cc-next" rel="next" href="/{i + 1}.html">Next</a>')
        page = f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Gannet Point - {title}</title>
<meta property="og:site_name" content="Gannet Point">
<meta property="og:title" content="{title}">
<meta property="og:image" content="{COMIC_HOST}/{i}.svg">
<meta property="article:published_time" content="{date.isoformat()}T09:00:00Z">
</head>
<body>
<h1>Gannet Point</h1>
<h2>{title}</h2>
<img id="cc-comic" src="/{i}.svg" alt="{title}">
<nav>{" ".join(nav)}</nav>
<p>Posted {date.strftime("%-d %B %Y")}.</p>
</body>
</html>
"""
        (out / f"{i}.html").write_text(page)
        if i == last:
            (out / "index.html").write_text(page)


def podcast(out):
    out.mkdir(parents=True, exist_ok=True)
    (out / "cover.svg").write_text(
        '<svg xmlns="http://www.w3.org/2000/svg" width="600" height="600"><rect width="600" height="600" fill="#1f5f8b"/>'
        '<path d="M0,380 q75,-40 150,0 t150,0 t150,0 t150,0 V600 H0z" fill="#7fb3d5"/>'
        '<circle cx="450" cy="140" r="70" fill="#f4f1ea"/>'
        '<text x="300" y="300" font-family="serif" font-size="72" fill="#fff" text-anchor="middle">Tide Tables</text></svg>')
    start = datetime.datetime(2018, 1, 9, 7, 0)
    items = []
    for i, (title, notes) in reversed(list(enumerate(EPISODES, 1))):
        when = start + datetime.timedelta(weeks=2 * (i - 1))
        (out / f"{i}.mp3").write_bytes(b"\xff\xfb\x90\x00" * 64)
        items.append(f"""<item>
<title>{i}. {title}</title>
<link>{PODCAST_HOST}/{i}</link>
<guid isPermaLink="false">tide-tables-{i}</guid>
<pubDate>{when.strftime('%a, %d %b %Y %H:%M:%S')} +0000</pubDate>
<description>{notes}</description>
<enclosure url="{PODCAST_HOST}/{i}.mp3" length="256" type="audio/mpeg"/>
<itunes:duration>{30 + i * 3 % 17}:00</itunes:duration>
<itunes:episode>{i}</itunes:episode>
</item>""")
    (out / "feed.xml").write_text(f"""<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd">
<channel>
<title>Tide Tables</title>
<link>{PODCAST_HOST}/</link>
<description>A fortnightly podcast about the sea going in and out.</description>
<image><url>{PODCAST_HOST}/cover.svg</url><title>Tide Tables</title><link>{PODCAST_HOST}/</link></image>
<itunes:image href="{PODCAST_HOST}/cover.svg"/>
<itunes:author>Two People and a Tide Gauge</itunes:author>
{"".join(items)}
</channel>
</rss>
""")
    (out / "index.html").write_text(
        '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Tide Tables</title>'
        '<link rel="alternate" type="application/rss+xml" title="Tide Tables" href="/feed.xml"></head>'
        '<body><h1>Tide Tables</h1><p>A fortnightly podcast about the sea going in and out.</p></body></html>')


if __name__ == "__main__":
    root = pathlib.Path(sys.argv[1])
    comic(root / "comic")
    podcast(root / "podcast")
