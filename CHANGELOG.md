# Changelog

All notable changes to this project are documented in this file. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this
project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Repository bootstrap: Go module, stub `andante-server` and `andante` binaries
  that report their version, Makefile, CI, lint, license check and Dependabot.
- Container image, security scanning (govulncheck, Trivy, gitleaks, CodeQL),
  dependency review and OpenSSF Scorecard.
- Release pipeline: reproducible archives, GitHub releases with an SBOM and build
  provenance, and a multi-arch image on ghcr.io.
- `andante-server` starts, migrates Postgres, creates a bootstrap API token, and
  serves `/healthz`, `/readyz` and `/metrics` on the admin listener.
- The v1 admin API contract, with a generated server and client. API tokens and
  sources work; other endpoints return 501 until they are built.
- Discovery: the `crawl` strategy, page extraction, a polite fetcher, probes, and
  a crawl loop that indexes lazily and watches the live end.
- Reads: RRULE and replay pacing, computed from an append-only history, with skip,
  pause, resume, jump and re-pacing.
- Feeds: Atom, RSS 2.0 and JSON Feed through per-person tokens, a "next one now"
  control for owners, and OPML export.

### Security

- Update `golang.org/x/net` to v0.62.0, for GO-2026-6860 and GO-2026-6866 in its
  HTML parser, which reads pages from other people's sites.
