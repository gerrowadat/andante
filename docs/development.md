# Development

## Prerequisites

- Go 1.21 or later, which fetches the toolchain named in `go.mod`.
- GCC or another C compiler, for the race detector in `make test`.
- Docker, for `make docker`, `make spec-lint`, and Postgres through `compose.yaml`.
- [Trivy](https://trivy.dev), for `make docker-scan` only.
- [Nomad](https://developer.hashicorp.com/nomad/install), for `make deploy-check` only.

`make lint` uses `golangci-lint` from your `PATH` if there is one, and otherwise fetches the
version CI pins. A different local version can disagree with CI.

## Make Targets

`make` on its own lists these.

| Target | What it does |
|---|---|
| `make build` | Builds `andante-server`, `andante-catalogue` and `andante` into `out/`, stamped with version, commit and date |
| `make run` | Starts the `compose.yaml` Postgres, then both services against it with text logs and a fixed `--secret` (`DEV_SECRET`) |
| `make test` | `go test -race -count=1 ./...`. Database tests skip |
| `make test-db` | The same tests against the `compose.yaml` Postgres, with no skips |
| `make e2e` | The end-to-end suite (build tag `e2e`), against the `compose.yaml` Postgres |
| `make e2e-browser` | The browser e2e tests only, with a pinned headless Chrome (see below) |
| `make dist` | The release archives, into `out/dist`; needs GNU tar |
| `make lint` | golangci-lint with `.golangci.yml` |
| `make spec-lint` | Spectral over `api/openapi.yaml`, in Docker as CI runs it |
| `make spec-diff` | Compares `api/openapi.yaml` with `BASE` (default `origin/main`) and fails on a breaking change |
| `make check` | Everything CI checks on a pull request, plus a secret scan: one line per step, and the failure for a step that fails. See `scripts/check.sh` |
| `make fmt` | `gofmt` over the tree |
| `make generate` | Regenerates both services' sqlc output and oapi-codegen code. Needs a C compiler. CI fails if the result differs |
| `make vendor-web` | Downloads the website's vendored CSS and JavaScript listed in `internal/web/static/vendor.json`, and checks their hashes |
| `make docker` | Builds the image for this machine's platform as `andante:dev` |
| `make docker-scan` | Builds that image and scans it as CI does; needs Trivy |
| `make deploy-check` | Validates the Compose stack and the Nomad job locally, and tests the Prometheus alerts; needs `nomad` and Docker |
| `make clean` | Removes `out/` |
| `make version` | Prints the version `make build` stamps |

## Tests That Need Postgres

Database tests use `storetest`, which gives each test its own schema. They skip when
`ANDANTE_TEST_DATABASE_URL` is unset. CI sets `ANDANTE_REQUIRE_DB=1`, which turns that skip into a
failure, so run `make test-db` before pushing anything that touches the database.

## Tests That Need a Browser

Two e2e tests drive Chrome through the website with chromedp. They look for Chrome or Chromium on
the `PATH`, or use the one `ANDANTE_E2E_CHROME` names, and skip if there isn't one. CI's e2e runs set
`ANDANTE_REQUIRE_BROWSER=1`, which turns that skip into a failure.

`make e2e-browser` runs just those tests and fails rather than skips. It uses
`scripts/chrome-headless.sh`, which downloads a pinned
[chrome-headless-shell](https://developer.chrome.com/blog/chrome-for-testing) into
`~/.cache/andante` (or `$ANDANTE_CACHE_DIR`), checks its hash, and prints its path. That's pinned for
Linux on x86_64 only; anywhere else, install Chrome and set `ANDANTE_E2E_CHROME`.

## Screenshots for the User Guide

The screenshots in the [user guide](user-guide.md) come from `make screenshots`, which runs
`scripts/guide/screenshots.sh`. It builds `andante:dev`, then runs andante, two made-up serials
(`scripts/guide/make_site.py`) and Miniflux in Docker on a network of their own, and drives Chromium
through them with Playwright (`scripts/guide/shoot.py`). It publishes no ports, so it doesn't clash
with an andante already running, and it removes everything when it's done. `KEEP=1` leaves the stack
up, and `IMAGE` uses an image you already have instead of building one.

Retake them when the website changes enough that they're wrong. They aren't checked in CI.

## Changing the Schema

Add a numbered file to `migrations/` and queries to `queries/`, under `internal/store/` for the server
or `internal/catalogue/store/` for the catalogue, and run `make generate`. `db/` is generated, so never
edit it by hand. Migrations are embedded in the binary and run when the service starts.

The compose Postgres only creates the catalogue's database on an empty volume. If `make run` can't
find it, run `docker compose down -v`.

## Changing the API

Change `api/openapi.yaml` first, then run `make generate`, then write the handler. Within `/api/v1`
changes are additive only, and `make spec-diff` shows whether yours is. `api/catalogue.yaml` works the
same way, but it's private, so it can change with the server that calls it.

`internal/stacktest` runs both services in one test, with a real sync between them.

## CI

CI runs on GitHub for every pull request. `make check` runs the same checks locally, and
`scripts/check.sh test lint` runs some of them. `CHECK_SKIP=e2e,spec-lint` leaves steps out.

`scripts/ci-wait.sh <pr>` waits for a pull request's checks without printing anything, then lists
only the checks that failed and any open code-scanning alerts.

## License Headers

Every source file needs the SPDX identifier and copyright line described in `CLAUDE.md`.
`scripts/license-check.sh` checks tracked files, so run it after `git add`.
