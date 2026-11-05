# Phase 10 — End-to-end suite, deployment, v0.1.0

Read first: `CLAUDE.md`, `docs/design/delivery.md`, `docs/design/` as a whole.
Reference for the Nomad job: gerrowadat/nomad-gitops `examples/` and
`docs/setup/running.md`.

## Goal

The release suite proves the whole system works, there is a documented way to
run it on Nomad, and v0.1.0 can be cut.

## Do

- Grow `test/e2e` (build tag `e2e`, run by `make e2e` and the release
  workflow): start Postgres (CI service, or compose locally) and the fixture
  sites; run the real `andante-server` binary as a subprocess with real flags;
  drive it only through `pkg/client` and plain HTTP to the public listener.
  Scenarios: (1) comic — probe → create source → create read with an RRULE →
  feeds for owner and viewer → "next one now" → pause/resume → restart the
  server and check positions and item IDs are unchanged → revoke a token;
  (2) podcast — create from feed → read with replay pacing at speed 7 → RSS
  feed has the right episodes on the right days, with enclosures;
  (3) OPML import → probe → enable. Use a server
  flag (test-only, documented as such) to inject a fake clock offset, rather
  than sleeping.
- `deploy/nomad/andante.nomad.hcl`: one task, the ghcr image, two ports
  (public and admin), DSN and admin token from Nomad Variables through a
  `template` block into env, service registrations with `/readyz` checks,
  resources sized modestly, `meta { gitops_managed = "true" }` so nomad-gitops
  can manage it. Postgres is assumed to exist already.
- `deploy/prometheus/alerts.yml`: source stuck in `error`, crawl failures
  sustained, feed 5xx rate, no successful crawl in 24 h.
- Docs: `docs/deploying.md` (Nomad, plain Docker, binary; exposing only the
  public listener via Tailscale Funnel or a Cloudflare Tunnel; backups with
  `pg_dump`), `docs/monitoring.md` (metrics, alerts, health endpoints),
  `docs/faq.md` (why my reader shows old dates — it doesn't, and why; why
  nothing crawls when I hit the feed; why a page was skipped).
- README: quick start (compose up, run, add a serial, subscribe), status line
  updated.
- `CHANGELOG.md`: a `0.1.0` section summarising what v0.1.0 contains.
- `git mv docs/proposals/architecture.md docs/design/architecture.md`, retitled
  and recording where it diverged, as `CLAUDE.md` describes.

## Out of scope

Anything new in the product. Helm, Kubernetes, systemd units.

## Acceptance criteria

- [ ] `make e2e` passes locally and in a `release.yml` dispatch with `publish=false`.
- [ ] `nomad job validate deploy/nomad/andante.nomad.hcl` passes (add it to CI if the Nomad CLI is cheap to install there).
- [ ] Every proposal that shipped is in `docs/design/`; `docs/proposals/` holds only unbuilt ideas; `docs/README.md` is current.

## Finish

Branch `phase-10-e2e-deploy`. PR. Stop. After merge, the human runs
`scripts/release.sh v0.1.0`.
