# Development

## Prerequisites

- Go 1.21 or later, which fetches the toolchain named in `go.mod`.
- GCC or another C compiler, for the race detector in `make test`.
- Docker, for `make docker`, `make spec-lint`, and Postgres through `compose.yaml`.
- [Trivy](https://trivy.dev), for `make docker-scan` only.

`make lint` uses `golangci-lint` from your `PATH` if there is one, and otherwise
fetches the version CI pins. A different local version can disagree with CI.

## Make targets

`make` on its own lists these.

| Target | What it does |
|---|---|
| `make build` | Builds `andante-server` and `andante` into `out/`, stamped with version, commit and date |
| `make run` | Starts the `compose.yaml` Postgres, then `andante-server` against it with text logs |
| `make test` | `go test -race -count=1 ./...`. Database tests skip |
| `make test-db` | The same tests against the `compose.yaml` Postgres, with no skips |
| `make e2e` | The end-to-end suite (build tag `e2e`), against the `compose.yaml` Postgres |
| `make dist` | The release archives, into `out/dist`; needs GNU tar |
| `make lint` | golangci-lint with `.golangci.yml` |
| `make spec-lint` | Spectral over `api/openapi.yaml`, in Docker as CI runs it |
| `make spec-diff` | Compares `api/openapi.yaml` with `BASE` (default `origin/main`) and fails on a breaking change |
| `make fmt` | `gofmt` over the tree |
| `make generate` | Regenerates `internal/store/db/` with sqlc, and `internal/api/gen/` and `pkg/client/` with oapi-codegen. Needs a C compiler. CI fails if the result differs |
| `make docker` | Builds the image for this machine's platform as `andante:dev` |
| `make docker-scan` | Builds that image and scans it as CI does; needs Trivy |
| `make clean` | Removes `out/` |
| `make version` | Prints the version `make build` stamps |

## Tests that need Postgres

Database tests use `storetest`, which gives each test its own schema. They skip
when `ANDANTE_TEST_DATABASE_URL` is unset. CI sets `ANDANTE_REQUIRE_DB=1`, which
turns that skip into a failure, so run `make test-db` before pushing a change
that touches the database.

## Changing the schema

Add a numbered file to `internal/store/migrations/`, put queries in
`internal/store/queries/`, and run `make generate`. `internal/store/db/` is
generated, so never edit it by hand. Migrations are embedded in the binary and
run when the server starts.

## Changing the API

Change `api/openapi.yaml` first, then run `make generate`, then write the
handler. Within `/api/v1` changes are additive only, and `make spec-diff` shows
whether yours is. When an operation stops returning 501, add it to `implemented`
in `internal/api/api_test.go`.

## CI

CI runs on GitHub for every pull request, and there is no local equivalent. The `make`
targets above cover the same checks one at a time.

## License headers

Every source file needs the SPDX identifier and copyright line described in
`CLAUDE.md`. `scripts/license-check.sh` checks tracked files, so run it after
`git add`.
