#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Dave O'Connor
#
# Keep `act --reuse` from running one checkout's CI against another's files.
#
# act names a job's container and volume after the workflow and job alone, so
# every checkout of this repository shares them, and a reused container keeps
# the workspace it was created with. A run can then report on files that are
# not in your tree, or come back green without looking at your code.
#
# Before act starts, this removes any act container whose workspace is a
# different checkout of this repository. Other projects' containers are left
# alone. With --clean it removes all of this repository's act containers and
# volumes, the current checkout's included, plus the given cache directory.
#
# Usage: act-guard.sh <workspace-dir>
#        act-guard.sh --clean <workspace-dir> [cache-dir]
set -euo pipefail

clean=false
if [ "${1-}" = "--clean" ]; then
	clean=true
	shift
fi

workspace=${1:?usage: act-guard.sh [--clean] <workspace-dir> [cache-dir]}
workspace=$(cd "$workspace" && pwd -P)
cache_dir=${2-}

if [ "$clean" = true ] && [ -n "$cache_dir" ]; then
	rm -rf "$cache_dir"
fi

if ! command -v docker >/dev/null 2>&1; then
	exit 0
fi

# Every checkout shares the primary checkout's .git, which is what tells a
# different checkout of this repository apart from somebody else's project.
common_dir=$(git rev-parse --git-common-dir 2>/dev/null || echo "")
if [ -z "$common_dir" ]; then
	exit 0
fi
repo_root=$(cd "$(dirname "$common_dir")" && pwd -P)

stale=""
for name in $(docker ps -a --filter "name=act-" --format "{{.Names}}" 2>/dev/null); do
	# act mounts the container's own named volume at the absolute path of the
	# directory the container was created from.
	dest=$(docker inspect "$name" \
		--format '{{range .Mounts}}{{if eq .Name "'"$name"'"}}{{.Destination}}{{end}}{{end}}' \
		2>/dev/null || true)
	[ -n "$dest" ] || continue
	if [ "$clean" = false ] && [ "$dest" = "$workspace" ]; then
		continue
	fi

	case "$dest" in
	"$repo_root" | "$repo_root"/*) stale="$stale $name" ;;
	esac
done

if [ -n "$stale" ]; then
	echo "==> act: removing container(s):"
	for name in $stale; do
		echo "      $name"
	done
	# shellcheck disable=SC2086 # deliberate word splitting of the name list
	docker rm -f $stale >/dev/null
	# docker cp never removes files, so a stale volume keeps deleted files
	# alive. Remove it along with its -env volume.
	for name in $stale; do
		docker volume rm -f "$name" "$name-env" >/dev/null 2>&1 || true
	done
fi
