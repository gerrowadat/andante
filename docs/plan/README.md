# Build plan

andante is built in phases. Each phase has a prompt in this directory, written
to be handed to Claude Code as-is. One phase is one PR (phase 4 may be two).
Review and merge each before starting the next; later prompts assume earlier
phases are on `main`.

## Before phase 0 (by hand)

1. Create `gerrowadat/andante` on GitHub (public, empty, no template files).
2. Copy this seed pack into it and make the one direct commit to `main`
   ("Seed: design docs and build plan"). Every change after this goes through
   a PR.
3. Repository settings:
   - [ ] Ruleset on `main`: require a PR, require the `ci` workflow's checks
         once phase 0 has added them, block force pushes.
   - [ ] Secret scanning and push protection on.
   - [ ] Private vulnerability reporting on.
   - [ ] Dependabot alerts and security updates on.
   - [ ] Actions: "Allow GitHub Actions to create and approve pull requests"
         off; default workflow permissions read-only.
4. Decisions you can still change before starting (edit the docs first if you do):
   - Name: `andante` — "at a walking pace" (binaries `andante-server` and
     `andante`, image `ghcr.io/gerrowadat/andante`). Checked on 2026-11-05: no
     notable GitHub project or self-hosted app by that name; the US marks found
     cover music-education software, a different field. EUIPO and the Irish
     register were not checked. Rejected: `trickle` (generic, an old Unix tool),
     `backissue` (an existing self-hosted comics app and a comics magazine),
     `rerun`, `feedrewind`, `dripfeed`, `cadence`, `tempo` (taken nearby).
   - License: Apache-2.0.
   - Server CLI library: kong (flags with env counterparts). CLI: cobra (shell
     completions; its own config is two env vars).

## Phases

| # | Prompt | Delivers |
|---|---|---|
| 0 | [00-bootstrap.md](00-bootstrap.md) | Go module, layout, stub binaries, Makefile, fast CI, lint, license check, act, Dependabot, repo hygiene files |
| 1 | [01-security.md](01-security.md) | Dockerfile, security scanning, CodeQL, dependency review, Scorecard, SHA-pinned actions |
| 2 | [02-release.md](02-release.md) | Reproducible archives, `release.sh`, release workflow, ghcr image, SBOM, provenance |
| 3 | [03-server-and-store.md](03-server-and-store.md) | kong config, two listeners, health/metrics, Postgres, migrations, sqlc, API token bootstrap |
| 4 | [04-api-contract.md](04-api-contract.md) | OpenAPI v1 spec, generated server and client, problem details, auth, contract checks in CI, sources CRUD |
| 5 | [05-discovery.md](05-discovery.md) | Strategy and kind-aware Extractor interfaces, polite crawler, `crawl` strategy, page extraction, probes, crawl loop, tail mode |
| 6 | [06-scheduling.md](06-scheduling.md) | `internal/schedule` with RRULE and replay pacing, reads, segments, adjustments |
| 7 | [07-feeds.md](07-feeds.md) | Feed tokens, Atom + RSS 2.0 (podcast-ready) + JSON Feed, decorators, "next one now", caching, OPML export |
| 8 | [08-cli.md](08-cli.md) | The `andante` CLI over `pkg/client` |
| 9 | [09-feeds-as-sources.md](09-feeds-as-sources.md) | Podcasts, blogs and news from feeds: `feed` (with RFC 5005), `feed-paged`, `numeric`, article and episode extractors, OPML import |
| 10 | [10-e2e-and-deploy.md](10-e2e-and-deploy.md) | End-to-end suite, Nomad job, operating docs, v0.1.0 |
| 11 | [11-catalogue.md](11-catalogue.md) | Split into `andante-catalogue` and `andante-server`, with a synced copy and demand |
| 12 | [12-users.md](12-users.md) | Users, personal API tokens, `user_id` on reads, a rewritten `/api/v1`, encrypted feed tokens |
| 13 | [13-login.md](13-login.md) | Password login, sessions, invites, CSRF, headers, rate limits, audit log |
| 14 | [14-pages.md](14-pages.md) | The web UI's pages, with per-user tests on every route |
| 15 | [15-add-a-site.md](15-add-a-site.md) | Discovery from a site's address, canonical keys, the add flow |
| 16 | [16-oidc-and-totp.md](16-oidc-and-totp.md) | TOTP and OIDC |
| 17 | [17-public-release.md](17-public-release.md) | Packaging, docs, end-to-end scenario, security review |

Phases 0–2 are plumbing only, so that every feature after them lands into a
repository that already tests, scans and releases itself.

## Running a phase

In a fresh Claude Code session at the repo root:

> Read `docs/plan/NN-….md` and carry it out.

Each prompt says what to read first, what is in and out of scope, and the
acceptance criteria. Each ends by opening a PR and stopping.

## After each phase

- Review the PR against the prompt's acceptance criteria.
- If a phase implemented part of a proposal, check the proposal was moved (or
  split) into `docs/design/`.
- If something turned out wrong in a later prompt, edit that prompt before
  running it; the prompts are plans, not contracts.
