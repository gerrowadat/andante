#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Dave O'Connor
#
# Download a pinned chrome-headless-shell from Chrome for Testing into a cache
# directory, check it against its SHA-256, and print the path of the binary.
# The browser e2e tests use it through ANDANTE_E2E_CHROME (see make e2e-browser).
# Downloads once per version; later runs only print the path.
#
# Only linux x86_64 is pinned. Elsewhere, install Chrome or Chromium and set
# ANDANTE_E2E_CHROME. To upgrade, change the version and the hash together.

set -eu

version=157.0.8103.78
sha256=f90329720fb61e5a76da9b6b509b6ad12a484c9c5df0359403e935177c6d153a

if [ "$(uname -s)-$(uname -m)" != Linux-x86_64 ]; then
	echo "chrome-headless.sh: only linux x86_64 is pinned; install Chrome and set ANDANTE_E2E_CHROME" >&2
	exit 1
fi

dir=${ANDANTE_CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/andante}/chrome-headless-shell-$version
bin=$dir/chrome-headless-shell-linux64/chrome-headless-shell

if [ ! -x "$bin" ]; then
	mkdir -p "$dir"
	zip=$dir/download.zip
	curl -fsSL -o "$zip" "https://storage.googleapis.com/chrome-for-testing-public/$version/linux64/chrome-headless-shell-linux64.zip"
	if ! echo "$sha256  $zip" | sha256sum -c --status; then
		echo "chrome-headless.sh: the download doesn't match the pinned hash" >&2
		rm -f "$zip"
		exit 1
	fi
	unzip -q -o "$zip" -d "$dir"
	rm -f "$zip"
fi

echo "$bin"
