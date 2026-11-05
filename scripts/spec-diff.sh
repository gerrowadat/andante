#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Dave O'Connor
#
# Fail if api/openapi.yaml breaks clients of the spec at $BASE. Within /api/v1
# changes are additive only, so a breaking change needs /api/v2, not a waiver.
#
# Usage: BASE=<commit> OASDIFF_VERSION=<version> scripts/spec-diff.sh

set -eu

cd "$(dirname "$0")/.."

base_spec=$(mktemp)
trap 'rm -f "$base_spec"' EXIT

if ! git show "$BASE:api/openapi.yaml" >"$base_spec" 2>/dev/null; then
	echo "==> $BASE has no api/openapi.yaml, so there is nothing to break"
	exit 0
fi

go run "github.com/oasdiff/oasdiff@$OASDIFF_VERSION" breaking "$base_spec" api/openapi.yaml --fail-on ERR
