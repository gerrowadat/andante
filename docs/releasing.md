# Releasing

A release runs the full suite and publishes nothing until it passes, including
the tag. `release.yml` does the work; `scripts/release.sh` starts it.

## Cutting a release

1. In a PR, move `CHANGELOG.md`'s `[Unreleased]` entries into a section for the
   new version, such as `## [0.1.0] - 2026-11-05`, and merge it. `release.sh`
   refuses to run without that section, and its text becomes the release notes.
2. On a clean `main`, level with `origin`, run:

   ```sh
   scripts/release.sh patch   # or minor, major, or an explicit vX.Y.Z
   ```

The script needs an authenticated `gh`. It works out the version from the latest
`vX.Y.Z` tag, runs `make test lint`, dispatches `release.yml` with the version
and commit, and watches the run.

## What each job does

| Job | Runs on | What it does |
|---|---|---|
| `full-suite` | Every trigger | Checks the version and commit, runs everything `ci.yml` runs, `make e2e` and `build-dist.sh` twice to prove the archives are reproducible, smoke-tests the archives, generates the SBOM |
| `publish` | Publishing only | Attests build provenance over the archives, creates the tag, creates the GitHub release with the archives, `SHA256SUMS` and the SBOM |
| `image` | Publishing only | Scans a linux/amd64 build with Trivy, failing on fixable CRITICAL or HIGH findings, then pushes linux/amd64 and linux/arm64 to `ghcr.io/gerrowadat/andante` tagged `X.Y.Z` and `X.Y` |

Publishing means `publish=true` on a dispatch, or a pushed `vX.Y.Z` tag. A pull
request runs `full-suite` alone, without version and commit validation, so
changes to the workflow prove themselves before merge. A dispatch without
`publish=true` also stops after the suite and publishes nothing:

```sh
gh workflow run release.yml --ref main -f publish=false
```

## Verifying an artifact

Download the archives and `SHA256SUMS` into one directory, then:

```sh
sha256sum -c SHA256SUMS
gh attestation verify andante-v0.1.0-linux-amd64.tar.gz --repo gerrowadat/andante
```

The archives are reproducible. `scripts/build-dist.sh` at the tagged commit,
with the same `VERSION` and the Go toolchain named in `go.mod`, produces
identical files. Pass the full commit as `COMMIT` if you build from anything
other than a checkout of it.

For the image, BuildKit attaches its SBOM and provenance attestations to the
image index:

```sh
docker buildx imagetools inspect ghcr.io/gerrowadat/andante:0.1.0
```

## Re-running a failed release

If `full-suite` failed, nothing was published. Fix the problem through a PR, merge
it, and run `release.sh` again for the same version.

If `publish` or `image` failed, the tag and possibly the release exist, and
`release.sh` refuses a version whose tag exists. Look at what the run published
first:

- If the failure is transient, re-run the failed jobs with
  `gh run rerun <run-id> --failed`. The tag step accepts a tag already at this
  commit. A release that already exists makes `gh release create` fail, so delete
  it first with `gh release delete vX.Y.Z` if it was created.
- If the commit itself is bad, leave the tag alone, fix the problem through a PR,
  and release the next patch version.
