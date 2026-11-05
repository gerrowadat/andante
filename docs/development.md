# Development

## Prerequisites

- Go 1.21 or later, which fetches the toolchain named in `go.mod`.
- GCC or another C compiler, for the race detector in `make test`.
- Docker, for `make ci`, `make docker`, and Postgres through `compose.yaml`.
- [act](https://github.com/nektos/act), for `make ci`. If it is not installed,
  `make ci` fetches the pinned version with `go run`.
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
| `make fmt` | `gofmt` over the tree |
| `make generate` | Regenerates `internal/store/db/` with sqlc. Needs a C compiler. CI fails if the result differs |
| `make docker` | Builds the image for this machine's platform as `andante:dev` |
| `make docker-scan` | Builds that image and scans it as CI does; needs Trivy |
| `make ci` | Runs the `ci` workflow under act |
| `make ci-clean` | Removes this repository's act containers, volumes and cache |
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

## Running CI locally

`make ci` runs `.github/workflows/ci.yml` in containers, using the image and
options in `.actrc`. The first run pulls an image of about 1GB. Later runs reuse
the containers, so run `make ci-clean` if a run reports on files you no longer
have.

`scripts/act-guard.sh` runs first. act names containers after the workflow and
job alone, so without the guard a run could use a container built for another
worktree.

The workflow also runs on GitHub for every pull request. `make ci` is most useful
when you are changing a workflow.

## License headers

Every source file needs the SPDX identifier and copyright line described in
`CLAUDE.md`. `scripts/license-check.sh` checks tracked files, so run it after
`git add`.
