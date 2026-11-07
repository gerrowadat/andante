# Design: Delivery

CI, security scanning and releases. Shipped in phases 0 to 2 of the
[build plan](../plan/README.md), with the end-to-end suite in phase 10. How to cut a release is in
[`releasing.md`](../releasing.md). This follows the patterns in gerrowadat/nomad-gitops and
gerrowadat/disgracelands, adjusted for a service with a Postgres dependency and an API contract.

## Principles

- **Fast CI on every commit** checks that it compiles, passes its tests, meets the lint bar, and
  keeps its contracts (generated code, API compatibility, licence headers).
- **Releases run the slow suite, and publish nothing until it passes,** including the tag.
  `scripts/release.sh` dispatches the release workflow with the version and commit, and the
  workflow only tags after the suite passes. A failed release leaves no trace.
- **Security scanning results go to the GitHub Security tab** as SARIF. Nobody reads workflow
  logs.
- **The image is the artifact that ships**, so it is the one gated on CVEs.

## Workflows

### `ci.yml`: Every Push to Main, Every PR

Concurrency group per ref, `cancel-in-progress` only for pull requests.
Top-level `permissions: contents: read`.

| Job | Steps |
|---|---|
| `test` | `go mod tidy` + diff; build; vet; gofmt; `go test -race -count=1 ./...` with a `postgres` service container, `ANDANTE_TEST_DATABASE_URL` set and `ANDANTE_REQUIRE_DB=1` |
| `lint` | golangci-lint v2, version pinned to match `.golangci.yml`'s schema |
| `contracts` | `make generate` then `git diff --exit-code` (oapi-codegen and sqlc output current); Spectral lint of `api/openapi.yaml`; on PRs, `oasdiff breaking` against the base branch's spec, failing on errors |
| `licenses` | `scripts/license-check.sh`: every tracked source file has its SPDX header |
| `workflows` | zizmor (GitHub Actions static analysis) over `.github/workflows/` |
| `deploy` | `make deploy-check` with a pinned, checksummed Nomad: the Compose stack, the Nomad job, and `promtool` over the alerts and their tests |

### `security.yml`: Push to Main, PRs, Weekly, Manual

| Job | What |
|---|---|
| `govulncheck` | Call-graph-aware Go vulnerability scan to SARIF. Fails on reachable findings |
| `trivy-fs` | Module scan to SARIF, report only |
| `trivy-image` | Build the image, scan it to SARIF, **fail on fixable CRITICAL/HIGH** |
| `gitleaks` | Secret scan of history |

### `codeql.yml`: Push to Main, PRs, Weekly

Standard Go CodeQL analysis.

### `dependency-review.yml`: PRs

`actions/dependency-review-action` fails PRs that add dependencies with known vulnerabilities or
incompatible licences.

### `scorecard.yml`: Weekly, Push to Main

OpenSSF Scorecard to SARIF, and it publishes results (the badge in the README).

### `release.yml`: Dispatched by `scripts/release.sh`, a Tag Push, or a PR

On a PR only `full-suite` runs, without version and commit validation, and only for the paths listed
in the workflow. A release always runs it.

1. `full-suite`: verify the commit matches the dispatched one; validate strict
   `vX.Y.Z`; everything in `ci.yml`; plus the end-to-end suite (a real
   `andante-server` against Postgres and the fixture sites, driven through
   `pkg/client`) and `scripts/build-dist.sh` (reproducible archives: CGO off,
   `-trimpath`, `-buildvcs=false`, mtimes and DATE from the commit, `TZ=UTC`;
   linux/amd64, linux/arm64, darwin/arm64 for both binaries; `SHA256SUMS`).
   Smoke-test the host archive (`andante version`, `andante-server --version`).
2. `publish` (needs full-suite): create the tag, the GitHub release with notes
   from `CHANGELOG.md`, attach archives and `SHA256SUMS`, generate an SPDX
   SBOM, and attest build provenance (`actions/attest-build-provenance`).
3. `image` (needs full-suite, publish): build multi-arch
   (linux/amd64, linux/arm64) with Go cross-compilation and no QEMU, scan with
   Trivy before pushing, push to `ghcr.io/gerrowadat/andante` with
   `{{version}}` and `{{major}}.{{minor}}` tags, BuildKit SBOM and provenance
   attestations, OCI labels.

## Supply Chain Details

- **Actions pinned by commit SHA** with the version in a trailing comment;
  Dependabot updates them. (A deliberate step beyond nomad-gitops and
  disgracelands, which pin by tag; Scorecard and zizmor both flag tag pins.)
- Every job declares minimal `permissions:`; `persist-credentials: false` on
  checkout unless the job pushes.
- No `pull_request_target`. Workflow inputs reach shell steps through `env:`,
  never `${{ }}` interpolation.
- Dependabot covers `gomod`, `github-actions` and `docker`, weekly, with grouped minor and patch
  updates.
- `go.mod` uses a `toolchain` line; `setup-go` reads `go-version-file: go.mod`.

## Container Image

Multi-stage: `golang` builder on `$BUILDPLATFORM` cross-compiling with
`CGO_ENABLED=0`; runtime `gcr.io/distroless/static-debian12:nonroot` (as
disgracelands does), with time zone data embedded via `import _ "time/tzdata"`.
Both binaries are in the image, and `andante-server` is the entrypoint. OCI labels. No shell means
no `apk upgrade` step to keep it clean of CVEs.

## Repository Settings

These are a manual, one-time checklist in `docs/plan/README.md`: a ruleset on `main` requiring `ci`
checks and PRs, secret scanning and push protection, private vulnerability reporting, and
Dependabot alerts and security updates. Merged branches are deleted automatically.

## Local Development

- `make help` (sectioned, disgracelands style), `make build`, `make test`,
  `make test-db` (starts Postgres in Docker and runs the integration tests),
  `make generate`, `make lint`, `make run` (server against local Postgres with
  dev flags).
- `compose.yaml` is Postgres only. The full stack is in `deploy/compose/`.

## Where This Diverged From the Proposal

- **Actions were pinned by SHA in phase 0, not phase 1.** zizmor reports tag pins
  as high-severity findings, so the `workflows` job could not pass otherwise.
- **Base images are pinned by digest as well as tag**, which goes beyond pinning
  actions.
- **`security.yml` and `release.yml` run on PRs.** GitHub only dispatches a
  workflow once its file is on the default branch, so a new or changed workflow
  could not otherwise prove itself before merge. On a PR, `release.yml` runs
  `full-suite` alone, without version and commit validation.
- **SARIF uploads skip PRs from forks**, whose token cannot write security events.
- **gitleaks scans history only on the scheduled and manual runs.** On a PR or a
  push it scans the commits in that event.
- **Scorecard's results appear under `supply-chain/*` categories**, whatever the
  upload step names.
- **`release.yml` only runs on PRs that need it.** Running it on all of them spent 8 minutes on docs
  fixes. It's a path filter, and not a `ci.yml` job, so it can't block a PR. Required checks can't be
  filtered this way, because a required check that never runs leaves a PR waiting for ever.
- **The image scan covers linux/amd64 only.** A multi-arch image cannot be loaded
  into the local daemon for Trivy to read.
- **The image has no `latest` tag**, only `X.Y.Z` and `X.Y`.
- **`release.sh` does not push.** disgracelands' does, but `main` here only takes
  PRs, so the changelog section is merged first and the script refuses without it.
- **`full-suite` runs without the setup-go cache**, so that a poisoned cache
  cannot reach what ships. zizmor flags it otherwise.
- **`full-suite` repeats `ci.yml`'s steps** instead of calling it as a reusable
  workflow, so that one job produces and checks the release files.
- **`act` was removed.** `make ci` wasn't run after phase 3, and the `contracts` job needs
  Docker-in-Docker there. CI runs on GitHub for every PR, which is enough.
- **Spectral runs from its Docker image**, pinned by digest, so `make spec-lint`
  and CI run the same thing and neither needs Node.
- **`oasdiff` skips when the base has no spec**, which only happened on the PR
  that added it.
- **`main` is protected by a repository ruleset**, not classic branch protection.
  It requires a PR, up-to-date branches and the checks `test`, `lint`, `licenses`, `workflows` and
  `contracts`, and blocks force pushes and deletion, with no bypass actors and no required
  approvals. Approvals can't work on a solo repository.
