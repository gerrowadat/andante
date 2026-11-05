# Phase 0 — Bootstrap the repository

Read first: `CLAUDE.md`, `docs/philosophy.md`, `docs/proposals/delivery.md`.
For reference patterns, look at github.com/gerrowadat/nomad-gitops
(`Makefile`, `.github/`, `CLAUDE.md`) and github.com/gerrowadat/disgracelands
(`Makefile` help format, `.actrc`, `scripts/act-guard.sh`,
`scripts/license-check.sh`, `.github/workflows/go.yml`). Adapt; do not copy
project-specific content.

## Goal

A repository that builds two stub binaries and runs fast CI on every PR. No
product code yet.

## Do

- `go.mod` for `github.com/gerrowadat/andante`, current stable Go with a
  `toolchain` line.
- Directory layout from `CLAUDE.md` with a `doc.go` in each `internal/`
  package stating its purpose (one paragraph, no code beyond `package x`).
- `internal/buildinfo` with `version`, `commit`, `date` set via ldflags, and a
  `String()` used by both binaries.
- `cmd/andante-server` and `cmd/andante`: each supports `--version` / `version` and
  nothing else yet. Use kong in `andante-server`, cobra in `andante`.
- `Makefile` with a sectioned `make help` (disgracelands style): `build`,
  `test`, `lint`, `fmt`, `generate` (no-op for now, wired later), `ci` (act),
  `ci-clean`, `clean`, `version`. Commands match what CI runs.
- `.golangci.yml` (v2 schema): errcheck, govet, ineffassign, staticcheck,
  unused, gosec, revive with a light ruleset; gofmt and goimports formatters.
  Test-file exclusions as in disgracelands.
- `scripts/license-check.sh`: every git-tracked `.go`, `.sh`, `.sql`, `.yml`,
  `.yaml`, `Makefile` and `Dockerfile` carries
  `SPDX-License-Identifier: Apache-2.0` and `Copyright (C) 2026 Dave O'Connor`.
  Markdown and generated files are exempt (generated files are identified by
  the standard `// Code generated … DO NOT EDIT.` line).
- `.github/workflows/ci.yml` with jobs `test` (tidy diff, build, vet, gofmt,
  `go test -race -count=1 ./...`), `lint` (golangci-lint, pinned version),
  `licenses`, `workflows` (zizmor). Concurrency group per ref,
  cancel-in-progress for PRs only, top-level `permissions: contents: read`,
  `persist-credentials: false`. `setup-go` reads `go-version-file: go.mod`.
- `.github/dependabot.yml`: gomod, github-actions; weekly; group minor+patch.
  The docker entry arrives with the Dockerfile in phase 1.
- `.actrc` and `scripts/act-guard.sh` (adapted from disgracelands); `make ci`
  runs the guard then `act`.
- `LICENSE` is already in the repository: the standard Apache-2.0 text, with
  the appendix placeholder left as it is. `SECURITY.md` (report via GitHub private
  vulnerability reporting; only the latest minor is supported),
  `CHANGELOG.md` (Keep a Changelog, `## [Unreleased]`),
  `.github/pull_request_template.md` (what/why/how tested, docs updated
  checkbox), `.editorconfig`, `.gitignore`.
- `compose.yaml` with Postgres only (current major), for local development
  in later phases.
- `docs/development.md`: prerequisites, make targets, running CI locally.

## Out of scope

Dockerfile, security workflows, release workflow (phases 1–2). Any product
behaviour.

## Acceptance criteria

- [ ] `make build test lint` pass locally; `make ci` runs the `ci` workflow under act.
- [ ] `andante-server --version` and `andante version` print version, commit and date.
- [ ] CI is green on the PR, and the license check fails if you remove a header (prove it in a throwaway commit, then drop that commit).
- [ ] `docs/README.md` links `docs/development.md`.

## Finish

Branch `phase-0-bootstrap`. Open the PR with a plain description. Stop.
After merge, the human adds the `ci` checks to the `main` ruleset.
