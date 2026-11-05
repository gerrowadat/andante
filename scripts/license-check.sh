#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Dave O'Connor
#
# Check that every tracked source file carries its SPDX identifier and
# copyright line. Markdown and generated files are exempt.

set -eu

cd "$(dirname "$0")/.."

SPDX='SPDX-License-Identifier: Apache-2.0'
COPYRIGHT="Copyright (C) 2026 Dave O'Connor"
fail=0

for f in $(git ls-files '*.go' '*.sh' '*.sql' '*.yml' '*.yaml' \
	':(glob)**/Makefile' ':(glob)**/Dockerfile'); do
	# Tracked but deleted from the working tree: nothing to check yet.
	[ -f "$f" ] || continue
	grep -Eq '^// Code generated .* DO NOT EDIT\.$' "$f" && continue

	if ! head -5 "$f" | grep -qF "$SPDX"; then
		echo "FAIL: $f has no SPDX identifier"
		fail=1
	fi
	if ! head -5 "$f" | grep -qF "$COPYRIGHT"; then
		echo "FAIL: $f has no copyright line"
		fail=1
	fi
done

if [ "$fail" -ne 0 ]; then
	echo "==> Add the header described in CLAUDE.md to the files above"
	exit 1
fi
echo "==> Every source file carries its header"
