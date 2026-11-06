#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Dave O'Connor
#
# Cross-compile the binaries for every platform a release ships,
# and package each pair with LICENSE, README.md and CHANGELOG.md.
#
# The archives are a function of the commit and VERSION: running this twice on
# one commit produces identical files, so anyone can check SHA256SUMS against
# their own build under the same Go toolchain. Binaries carry the commit's date
# and archive members its mtime, never the wall clock. Needs GNU tar.
#
# Usage: scripts/build-dist.sh [outdir]        # default: dist
# Environment: VERSION, COMMIT and DATE override what git reports.

set -eu

cd "$(dirname "$0")/.."

TZ=UTC
export TZ

OUT=${1:-dist}

VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
COMMIT=${COMMIT:-$(git rev-parse HEAD)}
EPOCH=$(git log -1 --format=%ct)
DATE=${DATE:-$(git log -1 --format=%cd --date=format-local:%Y-%m-%dT%H:%M:%SZ)}

BUILDINFO=github.com/gerrowadat/andante/internal/buildinfo
PLATFORMS="linux/amd64 linux/arm64 darwin/arm64"
BINARIES="andante-server andante-catalogue andante"
EXTRAS="LICENSE README.md CHANGELOG.md"

rm -rf "$OUT"
mkdir -p "$OUT"

echo "==> Building $VERSION ($(echo "$COMMIT" | cut -c1-7)) for: $PLATFORMS"

archives=""
for platform in $PLATFORMS; do
	os=${platform%/*}
	arch=${platform#*/}
	name="andante-$VERSION-$os-$arch"
	stage="$OUT/$name"

	# vet first: go build skips test files, which can stop compiling for one
	# platform while the binaries still build.
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go vet ./...

	mkdir -p "$stage"
	for bin in $BINARIES; do
		# -buildvcs=false because the three values that matter are passed
		# explicitly, and Go's own stamp varies with the working tree.
		CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
			go build -trimpath -buildvcs=false \
			-ldflags="-s -w \
				-X $BUILDINFO.version=$VERSION \
				-X $BUILDINFO.commit=$COMMIT \
				-X $BUILDINFO.date=$DATE" \
			-o "$stage/$bin" "./cmd/$bin"
	done
	# shellcheck disable=SC2086 # deliberate word splitting of the file list
	cp $EXTRAS "$stage/"

	# Set modes rather than inherit them from the builder's umask.
	find "$stage" -type f -exec chmod 0644 {} +
	chmod 0755 "$stage"
	for bin in $BINARIES; do
		chmod 0755 "$stage/$bin"
	done
	find "$stage" -exec touch -h -d "@$EPOCH" {} +

	# gzip -n keeps the timestamp and filename out of the header.
	tar --sort=name --owner=0 --group=0 --numeric-owner \
		-C "$OUT" -cf - "$name" | gzip -9n >"$OUT/$name.tar.gz"
	archives="$archives $name.tar.gz"
	rm -rf "$stage"
done

# shellcheck disable=SC2086 # deliberate word splitting of the archive list
(cd "$OUT" && sha256sum $archives >SHA256SUMS)

echo "==> $OUT:"
ls -l "$OUT"
