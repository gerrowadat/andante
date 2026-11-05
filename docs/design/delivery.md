# Design: Delivery — CI, security, releases

Status: shipped in phases 0–2 of the [build plan](../plan/README.md), except what
"Not built yet" lists. How to cut a release is in
[`releasing.md`](../releasing.md). Follows the patterns in
gerrowadat/nomad-gitops and gerrowadat/disgracelands, adjusted for a service with
a Postgres dependency and an API contract.

## Principles

- **Fast CI on every commit answers one question:** does this compile, pass
  its tests, meet the lint bar, and keep its contracts (generated code, API
  compatibility, license headers)?
- **Releases run the slow, thorough suite, and publish nothing until it
  passes** — including the tag. `scripts/release.sh` dispatches the release
  workflow with the version and commit; the workflow tags only after the suite
  passes. Failed releases leave no trace.
- **Security scanning results land in the GitHub Security tab** as SARIF, not
  in workflow logs nobody reads.
- **The image is the artifact that ships**, so it is the one gated on CVEs.
- **Workflows can be run locally with `act`** (`make ci`), the same as
  disgracelands, with `.actrc` and `act-guard.sh` for worktrees.

## Workflows

### `ci.yml` — every push to main, every PR

Concurrency group per ref, `cancel-in-progress` only for pull requests.
Top-level `permissions: contents: read`.

| Job | Steps |
|---|---|
| `test` | `go mod tidy` + diff; build; vet; gofmt; `go test -race -count=1 ./...` with a `postgres` service container, `ANDANTE_TEST_DATABASE_URL` set and `ANDANTE_REQUIRE_DB=1` |
| `lint` | golangci-lint v2, version pinned to match `.golangci.yml`'s schema |
| `contracts` | `make generate` then `git diff --exit-code` (oapi-codegen and sqlc output current); Spectral lint of `api/openapi.yaml`; on PRs, `oasdiff breaking` against the base branch's spec, failing on errors |
| `licenses` | `scripts/license-check.sh`: every tracked source file has its SPDX header |
| `workflows` | zizmor (GitHub Actions static analysis) over `.github/workflows/` |

### `security.yml` — push to main, PRs, weekly, manual

| Job | What |
|---|---|
| `govulncheck` | Call-graph-aware Go vulnerability scan → SARIF |
| `trivy-fs` | Module scan → SARIF, report only |
| `trivy-image` | Build the image, scan it → SARIF, **fail on fixable CRITICAL/HIGH** |
| `gitleaks` | Secret scan of history |

### `codeql.yml` — push to main, PRs, weekly

Standard Go CodeQL analysis.

### `dependency-review.yml` — PRs

`actions/dependency-review-action`: fail PRs that add dependencies with known
vulnerabilities or incompatible licenses.

### `scorecard.yml` — weekly, push to main

OpenSSF Scorecard → SARIF, and publish results (badge in README).

### `release.yml` — dispatched by `scripts/release.sh`, a tag push, or a PR

On a PR only `full-suite` runs, without version and commit validation.

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
   (linux/amd64, linux/arm64) with Go cross-compilation — no QEMU — scan with
   Trivy before pushing, push to `ghcr.io/gerrowadat/andante` with
   `{{version}}` and `{{major}}.{{minor}}` tags, BuildKit SBOM and provenance
   attestations, OCI labels.

## Supply chain details

- **Actions pinned by commit SHA** with the version in a trailing comment;
  Dependabot updates them. (A deliberate step beyond nomad-gitops and
  disgracelands, which pin by tag; Scorecard and zizmor both flag tag pins.)
- Every job declares minimal `permissions:`; `persist-credentials: false` on
  checkout unless the job pushes.
- No `pull_request_target`. Workflow inputs reach shell steps through `env:`,
  never `${{ }}` interpolation.
- Dependabot: `gomod`, `github-actions`, and `docker` once the Dockerfile lands
  in phase 1; weekly, grouped minor/patch updates.
- `go.mod` uses a `toolchain` line; `setup-go` reads `go-version-file: go.mod`.

## Container image

Multi-stage: `golang` builder on `$BUILDPLATFORM` cross-compiling with
`CGO_ENABLED=0`; runtime `gcr.io/distroless/static-debian12:nonroot` (as
disgracelands does), with time zone data embedded via `import _ "time/tzdata"`.
Both binaries in the image; `andante-server` is the entrypoint. OCI labels. No shell
means no `apk upgrade` step to keep CVE-clean.

## Repository settings (manual, once)

Recorded in `docs/plan/README.md` as a checklist: ruleset on `main` requiring
`ci` checks and PRs; secret scanning and push protection; private
vulnerability reporting; Dependabot alerts and security updates.

## Local development

- `make help` (sectioned, disgracelands style), `make build`, `make test`,
  `make test-db` (starts Postgres in Docker and runs the integration tests),
  `make generate`, `make lint`, `make ci` (act), `make run` (server against
  local Postgres with dev flags).
- `compose.yaml` for Postgres only.

## Where this diverged from the proposal

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
- **The image scan covers linux/amd64 only.** A multi-arch image cannot be loaded
  into the local daemon for Trivy to read.
- **The image has no `latest` tag**, only `X.Y.Z` and `X.Y`.
- **`release.sh` does not push.** disgracelands' does, but `main` here only takes
  PRs, so the changelog section is merged first and the script refuses without it.
- **`full-suite` runs without the setup-go cache**, so that a poisoned cache
  cannot reach what ships. zizmor flags it otherwise.
- **`full-suite` repeats `ci.yml`'s steps** instead of calling it as a reusable
  workflow, so that one job produces and checks the release files.
- **`make ci-clean` is a `--clean` mode of `act-guard.sh`**, not a separate
  `act-clean.sh`.
- **`main` is protected by a repository ruleset**, not classic branch protection.
  It requires a PR and the checks `test`, `lint`, `licenses` and `workflows`, and
  blocks force pushes and deletion, with no bypass actors and no required
  approvals.

## Not built yet

- **Phase 4:** Spectral and `oasdiff` in the `contracts` job.
- **Phase 10:** the end-to-end suite described above. Today `test/e2e` holds one
  test, that `andante-server --version` reports the stamped version.
