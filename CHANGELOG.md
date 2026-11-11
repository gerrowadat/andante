# Changelog

All notable changes to this project are documented in this file. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this
project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- `golang.org/x/crypto` is at a commit past GO-2026-5932, which flags every older version for
  its `openpgp` package. andante never imported it.
- A requested crawl, a deleted source or a parked source waking up could deadlock with a crawl in
  the catalogue, which failed one of them with a 500.

### Added

- Users. Sources are shared, and reads and tokens are private. `andante-server user add` and
  `user reset` manage them, and `GET /me` and `andante whoami` say who you are.
- Password login, with sessions, single-use invite links, throttling and an audit log.
  `andante invite`, `andante session` and `andante audit`, and `andante-server user link` for the
  first password. `andante-server user reset` now also ends sessions and clears the password.
- `GET /sources` takes `q` and `kind`, and an admin's `GET /reads` takes `all`.
- Feed URLs can be shown again, through `GET /reads/{id}/tokens` and `andante token list`.
  `andante-server rotate-secret` changes the key.

- The website: browse and add sources, track one at a pace, manage reads and feed links, users and
  your account. It needs no JavaScript.
- A feed URL with no extension is a page saying what the read is, how fast it goes and how far along
  it is. The feed's description says the pace too.
- Two-factor with TOTP and single-use recovery codes, for password logins. Admins can require it
  with `PATCH /settings` or `andante settings set`.
- Logging in with an OpenID provider (`--oidc-*`). People are matched on the provider's issuer and
  subject, never on email.
- `GET /users`, `PATCH /users/{id}` and `DELETE /users/{id}`, and `andante user list`, `user edit`
  and `user rm`, for admins.

- `andante source discover` and `POST /discoveries` work out how to read a site from its address.
- A serial can only be added once. `POST /sources` answers 409 with the existing source's ID.
- Creating a read on a paused source that has never been crawled starts it, so that anyone who imports
  OPML can start what they imported.

### Changed

- A source nobody reads stops being checked at the live end, and is checked again as soon as
  someone starts a read.

- The API moved to the public listener, and the CLI's default URL with it. `--admin-listen` is now
  `--ops-listen`, and only has health and metrics. `--secret` is required, and `--admin-token` is gone.
- andante is now two services. `andante-catalogue` holds sources and entries and does the crawling,
  and `andante-server` keeps a copy and owns reads, tokens and feeds. Each needs its own database,
  and there's no migration from v0.0.1. Crawl flags moved to `andante-catalogue`, and the server
  gains `--catalogue-url`, `--catalogue-token` and `--demand-interval`.

### Security

- Build with Go 1.27.4, for GO-2026-6859, where a race in `net/http`'s `MaxBytesReader` can crash an
  HTTP/1 server.

## [0.0.1] - 2026-11-06

The first release. andante indexes a serial's archive and replays it as a paced feed.
It reads archives from feeds, RFC 5005 archived feeds, paged feeds, numbered URLs and
by crawling, and it serves Atom, RSS 2.0 and JSON Feed. It comes with an admin API, a
CLI, a Nomad job and alert rules.

### Added

- Repository bootstrap: Go module, stub `andante-server` and `andante` binaries
  that report their version, Makefile, CI, lint, license check and Dependabot.
- Container image, security scanning (govulncheck, Trivy, gitleaks, CodeQL),
  dependency review and OpenSSF Scorecard.
- Release pipeline: reproducible archives, GitHub releases with an SBOM and build
  provenance, and a multi-arch image on ghcr.io.
- `andante-server` starts, migrates Postgres, creates a bootstrap API token, and
  serves `/healthz`, `/readyz` and `/metrics` on the admin listener.
- The v1 admin API contract, with a generated server and client.
- Discovery: the `crawl` strategy, page extraction, a polite fetcher, probes, and
  a crawl loop that indexes lazily and watches the live end.
- Reads: RRULE and replay pacing, computed from an append-only history, with skip,
  pause, resume, jump and re-pacing.
- Feeds: Atom, RSS 2.0 and JSON Feed through per-person tokens, a "next one now"
  control for owners, and OPML export.
- The `andante` CLI, covering the whole API, and `GET /api/v1/version`.
- An end-to-end suite that drives the real server through a comic, a podcast and an
  OPML import. It uses `--test-clock-offset`, which is for tests only.
- A Docker Compose stack, a Nomad job, Prometheus alert rules, and guides for running,
  deploying and monitoring.
- Feeds as sources: the `feed`, `feed-paged` and `numeric` strategies, RFC 5005
  archived feeds, `article` and `episode` extractors, opt-in full text, and OPML
  import. The CLI's `source probe` and `source add` take `--strategy`.

### Security

- The `security` workflow fails when `govulncheck` finds a vulnerability in code
  andante calls.
- Update `golang.org/x/net` to v0.62.0, for GO-2026-6860 and GO-2026-6866 in its
  HTML parser, which reads pages from other people's sites.
