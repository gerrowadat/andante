#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Dave O'Connor
#
# Cut a release: work out the next version, run the local checks, then hand the
# version to .github/workflows/release.yml and wait for it.
#
# Nothing here tags or publishes. The workflow does that, and only after its
# suite passes, so a failed release leaves no trace and the version stays free
# for the next attempt. Nothing here pushes either: main takes changes through
# PRs, so the changelog section for the release is merged beforehand.
#
# Usage: scripts/release.sh patch|minor|major|vX.Y.Z

set -eu

cd "$(dirname "$0")/.."

usage() {
	echo "Usage: $0 patch|minor|major|vX.Y.Z" >&2
	exit 2
}

die() {
	echo "release.sh: $*" >&2
	exit 1
}

[ $# -eq 1 ] || usage
ARG=$1

# 1. Preconditions. A release from anything but a clean main, level with origin,
# is one nobody can reproduce from history.
command -v gh >/dev/null 2>&1 || die "gh (the GitHub CLI) is required"
gh auth status >/dev/null 2>&1 || die "gh is not authenticated; run 'gh auth login'"

branch=$(git rev-parse --abbrev-ref HEAD)
[ "$branch" = "main" ] || die "on branch '$branch', not main"

if [ -n "$(git status --porcelain)" ]; then
	git status --short >&2
	die "working tree is not clean"
fi

git fetch origin main --tags >/dev/null 2>&1 || die "could not fetch from origin"
head=$(git rev-parse HEAD)
[ "$head" = "$(git rev-parse origin/main)" ] || die "local main is not level with origin/main"

# 2. The next version, from the latest vX.Y.Z tag. No tag means a first release.
current=$(git tag --list --sort=-v:refname 'v[0-9]*.[0-9]*.[0-9]*' |
	grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true)
current=${current:-v0.0.0}
current=${current#v}
major=$(echo "$current" | cut -d. -f1)
minor=$(echo "$current" | cut -d. -f2)
patch=$(echo "$current" | cut -d. -f3)

case "$ARG" in
patch) next="$major.$minor.$((patch + 1))" ;;
minor) next="$major.$((minor + 1)).0" ;;
major) next="$((major + 1)).0.0" ;;
*)
	printf '%s' "$ARG" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || usage
	next=${ARG#v}
	;;
esac
tag="v$next"

[ -z "$(git tag --list "$tag")" ] && [ -z "$(git ls-remote --tags origin "refs/tags/$tag")" ] ||
	die "$tag already exists"

awk -v want="## [$next]" 'index($0, want) == 1 { found = 1 } END { exit !found }' CHANGELOG.md ||
	die "CHANGELOG.md has no '## [$next]' section; move [Unreleased] to it in a PR first"

echo "==> Releasing $tag (was v$current) at $head"

# 3. A chance to fail here, before a runner spends minutes reaching the same
# answer. The workflow's suite is the gate.
make test lint

# 4. Hand over. gh workflow run prints nothing identifying the run, so find it
# as the first one newer than the newest that existed beforehand.
before=$(gh run list --workflow=release.yml --limit 1 --json databaseId --jq '.[0].databaseId // 0')

echo "==> Dispatching release.yml"
gh workflow run release.yml --ref main -f version="$tag" -f commit="$head" -f publish=true

run_id=""
waited=0
while [ "$waited" -lt 120 ]; do
	run_id=$(gh run list --workflow=release.yml --event=workflow_dispatch --limit 20 \
		--json databaseId \
		--jq "[.[] | select(.databaseId > $before)] | sort_by(.databaseId) | .[0].databaseId // empty")
	[ -n "$run_id" ] && break
	sleep 3
	waited=$((waited + 3))
done
[ -n "$run_id" ] || die "dispatched, but no run appeared; find it with: gh run list --workflow=release.yml"

echo "==> Watching run $run_id"
gh run watch "$run_id" --exit-status ||
	die "the release run failed. See: gh run view $run_id --log-failed. Check what it published before re-running."

git fetch origin --tags >/dev/null 2>&1 || true
echo "==> Released $tag"
gh release view "$tag" --json url --jq '.url'
