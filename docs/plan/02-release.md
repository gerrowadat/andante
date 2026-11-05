# Phase 2 — Release pipeline

Read first: `CLAUDE.md`, `docs/proposals/delivery.md` ("release.yml",
"Supply chain details"). Reference: gerrowadat/disgracelands
`scripts/release.sh`, `scripts/build-dist.sh`, `.github/workflows/release.yml`
— in particular the reasoning about tagging only after the suite passes,
reproducible archives, `value=` on metadata-action tags, and cross-compiling
the arm64 image without QEMU.

## Goal

`scripts/release.sh patch|minor|major|vX.Y.Z` cuts a release: the full suite
runs, and only if it passes are the tag, GitHub release, archives, SBOM,
provenance and multi-arch image published.

## Do

- `scripts/build-dist.sh [outdir]`: builds `andante-server` and `andante` for
  linux/amd64, linux/arm64, darwin/arm64 with `CGO_ENABLED=0 -trimpath
  -buildvcs=false`; packages each platform with LICENSE, README and
  CHANGELOG as `.tar.gz`; mtimes and DATE from the commit; `TZ=UTC`; writes
  `SHA256SUMS`. Running it twice on the same commit produces identical files
  (add a test or a CI step that proves it).
- `scripts/release.sh`: preconditions (gh installed and authenticated, on
  main, clean, level with origin); compute the next version from the latest
  `vX.Y.Z` tag; check `CHANGELOG.md` has a section for it (move
  `[Unreleased]` → the version and date, committed through a PR beforehand —
  the script refuses if the section is missing); run `make test lint`; dispatch
  `release.yml` with `version`, `commit`, `publish=true`; watch it with `gh`.
- `make e2e` and `test/e2e/` with build tag `e2e`: for now one test that starts
  `andante-server` as a subprocess and checks `--version`. Phase 10 grows it.
- `.github/workflows/release.yml`:
  - `full-suite`: version and commit validation (inputs via `env:`); everything
    `ci.yml` runs; `make e2e`; `build-dist.sh`; smoke-test the linux/amd64
    archive; `sha256sum -c`.
  - `publish` (needs full-suite): create and push the tag (skip if it exists,
    for the tag-push path); GitHub release with that version's
    CHANGELOG section as notes; upload archives and `SHA256SUMS`; SPDX SBOM
    for the source tree attached to the release;
    `actions/attest-build-provenance` over the archives.
  - `image` (needs full-suite, publish): buildx without QEMU; build, Trivy
    scan (fail on fixable CRITICAL/HIGH), then push to
    `ghcr.io/gerrowadat/andante` with `{{version}}` and `{{major}}.{{minor}}`
    tags (`value=` from full-suite's output), `provenance: mode=max`,
    `sbom: true`, OCI labels from metadata-action.
  - `workflow_dispatch` without `publish=true` runs the suite only.
  - A permanent `pull_request` trigger runs `full-suite` alone, skipping version
    and commit validation because a PR has no inputs. `publish` and `image` are
    gated on the event not being `pull_request`. GitHub only dispatches a
    workflow once its file is on the default branch, so this is how the
    workflow proves itself before merge.
- `docs/releasing.md`: how to cut a release, what each job does, how to verify
  an artifact (`gh attestation verify`, `sha256sum -c`), how to re-run a failed
  release.

## Out of scope

Signing with cosign keys (attestations cover provenance). Homebrew or other
package managers.

## Acceptance criteria

- [ ] On the PR, `full-suite` is green and `publish` and `image` are skipped.
- [ ] After merge (human-run), `workflow_dispatch` with `publish=false` on main runs green and publishes nothing.
- [ ] `build-dist.sh` is reproducible (proved in CI).
- [ ] zizmor still passes; all actions SHA-pinned.
- [ ] After merge, a `v0.0.1` dry run (human-run) produces a release with archives, SHA256SUMS, SBOM, attestations and an image whose `andante-server --version` reports v0.0.1.

## Finish

Branch `phase-2-release`. PR. Stop. Move the delivery proposal to
`docs/design/delivery.md` in this PR (phases 0–2 complete it), noting any
divergence.
