# Development

## Prerequisites

- Go 1.21 or later, which fetches the toolchain named in `go.mod`.
- GCC or another C compiler, for the race detector in `make test`.
- Docker, for `make ci` and for Postgres through `compose.yaml` in later phases.
- [act](https://github.com/nektos/act), for `make ci`. If it is not installed,
  `make ci` fetches the pinned version with `go run`.

`make lint` uses `golangci-lint` from your `PATH` if there is one, and otherwise
fetches the version CI pins. A different local version can disagree with CI.

## Make targets

`make` on its own lists these.

| Target | What it does |
|---|---|
| `make build` | Builds `andante-server` and `andante` into `out/`, stamped with version, commit and date |
| `make test` | `go test -race -count=1 ./...`, as CI runs it |
| `make lint` | golangci-lint with `.golangci.yml` |
| `make fmt` | `gofmt` over the tree |
| `make generate` | Regenerates generated code; nothing to do yet |
| `make ci` | Runs the `ci` workflow under act |
| `make ci-clean` | Removes this repository's act containers, volumes and cache |
| `make clean` | Removes `out/` |
| `make version` | Prints the version `make build` stamps |

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
