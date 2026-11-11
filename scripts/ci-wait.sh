#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Dave O'Connor
#
# Waits for a pull request's checks to finish, saying nothing while it waits,
# then prints only what needs looking at: the checks that didn't pass, and any
# open code-scanning alerts on the PR's merge ref (leaving out notes, such as a
# vulnerable module nothing calls). Prints "all checks passed"
# and exits 0 if there's nothing; exits 1 otherwise.
#
#   scripts/ci-wait.sh 46
#
# Run it in the background and get on with something else.
set -u

pr=${1:?usage: ci-wait.sh <pr>}
interval=${CI_WAIT_INTERVAL:-30}

# Checks can take a few seconds to register after a push, and --watch returns at
# once if there are none yet.
sleep "${CI_WAIT_SETTLE:-20}"
gh pr checks "$pr" --watch --interval "$interval" >/dev/null 2>&1

bad=$(gh pr checks "$pr" --json name,bucket,link \
	--jq '.[] | select(.bucket != "pass" and .bucket != "skipping") | "\(.bucket)\t\(.name)\t\(.link)"' | sort -u)
repo=$(gh repo view --json nameWithOwner --jq .nameWithOwner)
alerts=$(gh api "repos/$repo/code-scanning/alerts?ref=refs/pull/$pr/merge&state=open" \
	--jq '.[] | select(.rule.severity != "note") | "\(.rule.id)\t\(.most_recent_instance.location.path):\(.most_recent_instance.location.start_line)\t\(.most_recent_instance.message.text)"' 2>/dev/null |
	cut -c1-240)

if [ -z "$bad" ] && [ -z "$alerts" ]; then
	echo "all checks passed"
	exit 0
fi
[ -z "$bad" ] || printf 'checks:\n%s\n' "$bad"
[ -z "$alerts" ] || printf 'code scanning:\n%s\n' "$alerts"
exit 1
