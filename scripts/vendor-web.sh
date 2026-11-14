#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Dave O'Connor
#
# Download the website's vendored files listed in internal/web/static/vendor.json
# and check each one against its SHA-256. To upgrade a library, change its
# version, path, URL and hash there and run this. A test fails if the files and
# vendor.json disagree, so CI catches a file changed by hand.

set -eu

cd "$(dirname "$0")/../internal/web/static"

jq -r '.files[] | [.path, .url, .sha256] | @tsv' vendor.json |
	while IFS="$(printf '\t')" read -r path url sum; do
		mkdir -p "$(dirname "$path")"
		curl -fsSL -o "$path.tmp" "$url"
		if ! echo "$sum  $path.tmp" | sha256sum -c --status; then
			echo "FAIL: $url doesn't match the hash in vendor.json" >&2
			rm -f "$path.tmp"
			exit 1
		fi
		mv "$path.tmp" "$path"
		echo "$path"
	done
