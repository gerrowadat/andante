# Build plan

andante is built in phases. Each phase has a prompt in this directory, written
to be handed to Claude Code as-is. One phase is one PR (phase 4 may be two).
Review and merge each before starting the next; later prompts assume earlier
phases are on `main`.

## Status

Every phase below is merged, and v0.1.0 is the first release with all of them. The prompts are kept
as they were run, except that paths to docs that have moved since point at where they are now.

What isn't built is in [`../proposals/`](../proposals/). Open questions for the maintainer are
GitHub issues labelled `maintainer-question`.

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

| # | Prompt | Delivers | PR |
|---|---|---|---|
| 0 | [00-bootstrap.md](00-bootstrap.md) | Go module, layout, stub binaries, Makefile, fast CI, lint, license check, act, Dependabot, repo hygiene files | [#1](https://github.com/gerrowadat/andante/pull/1) |
| 1 | [01-security.md](01-security.md) | Dockerfile, security scanning, CodeQL, dependency review, Scorecard, SHA-pinned actions | [#4](https://github.com/gerrowadat/andante/pull/4) |
| 2 | [02-release.md](02-release.md) | Reproducible archives, `release.sh`, release workflow, ghcr image, SBOM, provenance | [#5](https://github.com/gerrowadat/andante/pull/5) |
| 3 | [03-server-and-store.md](03-server-and-store.md) | kong config, two listeners, health/metrics, Postgres, migrations, sqlc, API token bootstrap | [#6](https://github.com/gerrowadat/andante/pull/6) |
| 4 | [04-api-contract.md](04-api-contract.md) | OpenAPI v1 spec, generated server and client, problem details, auth, contract checks in CI, sources CRUD | [#8](https://github.com/gerrowadat/andante/pull/8) |
| 5 | [05-discovery.md](05-discovery.md) | Strategy and kind-aware Extractor interfaces, polite crawler, `crawl` strategy, page extraction, probes, crawl loop, tail mode | [#9](https://github.com/gerrowadat/andante/pull/9) |
| 6 | [06-scheduling.md](06-scheduling.md) | `internal/schedule` with RRULE and replay pacing, reads, segments, adjustments | [#11](https://github.com/gerrowadat/andante/pull/11) |
| 7 | [07-feeds.md](07-feeds.md) | Feed tokens, Atom + RSS 2.0 (podcast-ready) + JSON Feed, decorators, "next one now", caching, OPML export | [#12](https://github.com/gerrowadat/andante/pull/12) |
| 8 | [08-cli.md](08-cli.md) | The `andante` CLI over `pkg/client` | [#13](https://github.com/gerrowadat/andante/pull/13) |
| 9 | [09-feeds-as-sources.md](09-feeds-as-sources.md) | Podcasts, blogs and news from feeds: `feed` (with RFC 5005), `feed-paged`, `numeric`, article and episode extractors, OPML import | [#21](https://github.com/gerrowadat/andante/pull/21) |
| 10 | [10-e2e-and-deploy.md](10-e2e-and-deploy.md) | End-to-end suite, Nomad job, operating docs. Released as v0.0.1 (#25) | [#23](https://github.com/gerrowadat/andante/pull/23) |
| 11 | [11-catalogue.md](11-catalogue.md) | Split into `andante-catalogue` and `andante-server`, with a synced copy and demand | [#33](https://github.com/gerrowadat/andante/pull/33) |
| 12 | [12-users.md](12-users.md) | Users, personal API tokens, `user_id` on reads, a rewritten `/api/v1`, encrypted feed tokens | [#37](https://github.com/gerrowadat/andante/pull/37) |
| 13 | [13-login.md](13-login.md) | Password login, sessions, invites, CSRF, headers, rate limits, audit log | [#44](https://github.com/gerrowadat/andante/pull/44) |
| 14 | [14-pages.md](14-pages.md) | The web UI's pages, with per-user tests on every route | [#46](https://github.com/gerrowadat/andante/pull/46) |
| 15 | [15-add-a-site.md](15-add-a-site.md) | Discovery from a site's address, canonical keys, the add flow | [#43](https://github.com/gerrowadat/andante/pull/43) |
| 16 | [16-oidc-and-totp.md](16-oidc-and-totp.md) | TOTP and OIDC | [#50](https://github.com/gerrowadat/andante/pull/50) |
| 17 | [17-bootstrap.md](17-bootstrap.md) | A bootstrap token for the first admin, `/setup` and `POST /bootstrap` | [#54](https://github.com/gerrowadat/andante/pull/54) |
| 18 | [18-invites.md](18-invites.md) | Invites from users, links for more than one person, who invited whom | [#55](https://github.com/gerrowadat/andante/pull/55) |
| 19 | [19-public-release.md](19-public-release.md) | Packaging, docs, end-to-end scenario, security review | [#57](https://github.com/gerrowadat/andante/pull/57) |

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
