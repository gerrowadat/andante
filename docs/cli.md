# CLI

`andante` manages a server through its API, and that's all it does. Anything it can do,
you can do with the API directly.

## Install

It's in every [release archive](releasing.md) and in the container image, alongside
`andante-server`. To use the one in the image, see [installing](install.md#the-cli). From source:

```sh
make build   # out/andante
```

## Connecting

| Flag | Env | Default |
|---|---|---|
| `--url` | `ANDANTE_URL` | `http://127.0.0.1:8080` |
| `--token` | `ANDANTE_TOKEN` | none |

Inside the server's container, where `ANDANTE_BASE_URL` is set, the default is the server's own
listener (`ANDANTE_LISTEN`, on loopback) under the base URL's path, so the CLI in the image finds
the server without a `--url`.

Use the environment variable for the token, which keeps it out of process listings. There's no
config file, and nothing is stored on disk.

## Output

Commands print tables. `-o json` prints the API's response body unchanged, for scripts:

```sh
andante source list -o json | jq '.items[].slug'
```

`export opml` and `api spec` always print the document itself. Lists show one page, and print
`more: --cursor …` to stderr if there is another. They take `--limit` and `--cursor`.

Errors go to stderr, and the exit code is non-zero:

```text
Error: Bad Request: the request does not match the API description
  /slug: does not match pattern
```

## Sources

A source is given by its ID or its slug.

```sh
andante source discover https://comic.example/                # work out how to read a site
andante source probe https://comic.example/1                  # what would be indexed, saving nothing
andante source probe https://comic.example/1 --selector next=a.forward --limit 5
andante source add https://comic.example/1 --slug my-comic --title "My Comic"
andante source list
andante source get my-comic
andante source edit my-comic --title "A Better Title"
andante source edit my-comic --pause
andante source edit my-comic --resume                         # also restarts a source in error
andante source probe https://pod.example/rss --strategy feed --kind episode
andante source probe 'https://comic.example/{n}' --strategy numeric --start-id 1
andante source edit my-comic --selector image="#strip img"    # replaces the selectors
andante source crawl my-comic                                 # ask for a crawl now
andante source entries my-comic
andante source rm my-comic
```

`--selector` takes `field=css`, where the field is `title`, `image`, `alt_text`, `date` or `next`.

`source probe` and `source add` take `--strategy` (`crawl`, `feed`, `feed-paged` or
`numeric`), and the URL argument is that strategy's own: a start page, a feed, or a
template with `{n}`. The other flags are `--full-text`, `--fill-gaps`, `--max-pages`,
`--page-param`, `--start-id`, `--max-misses`, `--format` and `--field`. See
[sources](sources.md). `source edit --start-url` changes the same URL on any strategy.

## Reads

`read create` takes a source and one pacing. The shorthand gets turned into an RRULE here, because
the API only takes RRULEs.

```sh
andante read create my-comic --daily 07:00 --tz Europe/Dublin
andante read create my-comic --weekdays 07:00 --count 3
andante read create my-comic --days MO,WE,FR --at 08:00
andante read create my-comic --rrule 'FREQ=WEEKLY;BYDAY=FR;BYHOUR=17' --count 2
andante read create my-podcast --replay --speed 7 --max-gap P3D
```

| Flag | Meaning |
|---|---|
| `--name` | Name of the read |
| `--start` | Where to start: `first`, a seq, or an entry's URL |
| `--initial` | Entries there immediately |
| `--tz` | IANA time zone, by default UTC |
| `--count` | Entries per occurrence |
| `--speed`, `--min-gap`, `--max-gap`, `--default-gap` | For `--replay`. Gaps are ISO 8601 durations |
| `--catch-up`, `--buffer` | What happens at the end of the index. Fixed once the read exists |

```sh
andante read list
andante read get 1
andante read edit 1 --name Evenings --config '{"window": 50}'
andante read pause 1
andante read resume 1
andante read skip 1                    # release the next entry now
andante read skip 1 3
andante read jump 1 40                 # release everything up to seq 40
andante read pace 1 --daily 18:00      # takes the same pacing flags as create
andante read history 1
andante read rm 1
```

See [reads](reads.md).

## Feed Tokens

```sh
andante token add 1 --label Alex              # a viewer token
andante token add 1 --label me --owner        # with the next-one-now control
andante token list 1                          # with each one's URL
andante token rm 3
```

See [feeds](feeds.md).

## OPML

```sh
andante export opml --issue viewer --label "For Alex" > feeds.opml
andante import opml feeds.opml
```

Each export issues a new token per read.

## API Tokens

```sh
andante api-token add --label laptop     # shown only once
andante api-token list
andante api-token rm 2
```

## Logins

```sh
andante invite add alex                  # a link for alex, used once
andante invite add --uses 5 --days 14    # five people, each picking a name
andante invite add pat --admin           # admins only
andante invite add alex --reset          # a new password for an existing user. Admins only
andante invite list                      # yours, or everyone's for an admin
andante invite rm 3                      # withdraw it
andante session list
andante session rm 4                     # log that browser out
andante audit                            # admins only
andante settings                         # admins only
andante settings set --require-two-factor
andante settings set --who-can-invite users --invite-limit 5
andante settings set --password-min-length 12 --password-classes 3
```

## Users

Admins only.

```sh
andante user list
andante user edit 3 --role admin
andante user edit 3 --disable            # logs them out and stops their API tokens
andante user edit 3 --enable
andante user rm 3 --yes                  # their reads and feed links go too
```

Disabling someone leaves their feed links working, so that friends on a group read aren't cut off.
`andante token rm` stops them one at a time.

## The API Itself

```sh
andante api spec > openapi.json
andante version                          # the client's, and the server's when reachable
andante whoami
```

## Completions

```sh
andante completion bash > /etc/bash_completion.d/andante
andante completion zsh  > "${fpath[1]}/_andante"
andante completion fish > ~/.config/fish/completions/andante.fish
```
