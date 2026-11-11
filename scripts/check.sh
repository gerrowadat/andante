#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Dave O'Connor
#
# Runs every check worth running before a push, and says little: one line per
# step, and the end of the log only for a step that fails. The exit status is
# non-zero if any step failed. Full logs stay in $CHECK_LOGS.
#
#   scripts/check.sh                 # everything
#   scripts/check.sh test lint       # only these steps
#   CHECK_SKIP=e2e,spec-lint scripts/check.sh
#
# Tests run against ANDANTE_TEST_DATABASE_URL, or the compose Postgres on
# 127.0.0.1:5432 if that's unset, with skips turned into failures as in CI.
set -u

cd "$(dirname "$0")/.." || exit 2

ALL=(generate test e2e lint spec-lint spec-diff deploy-check license govulncheck gitleaks)
STEPS=("$@")
[ ${#STEPS[@]} -gt 0 ] || STEPS=("${ALL[@]}")
SKIP=",${CHECK_SKIP:-},"
BASE=${BASE:-origin/main}
TAIL=${CHECK_TAIL:-30}
GITLEAKS_IMAGE=${GITLEAKS_IMAGE:-zricethezav/gitleaks:v8.30.1}
CHECK_LOGS=${CHECK_LOGS:-$(mktemp -d "${TMPDIR:-/tmp}/andante-check.XXXXXX")}
mkdir -p "$CHECK_LOGS"

export ANDANTE_TEST_DATABASE_URL=${ANDANTE_TEST_DATABASE_URL:-postgres://andante:andante@127.0.0.1:5432/andante?sslmode=disable}
export ANDANTE_REQUIRE_DB=1

generated=(internal/api/gen pkg/client internal/store/db internal/catalogue)

step_generate() {
	local before after
	before=$(find "${generated[@]}" -name '*.go' -type f -print0 | sort -z | xargs -0 sha256sum)
	make generate || return 1
	after=$(find "${generated[@]}" -name '*.go' -type f -print0 | sort -z | xargs -0 sha256sum)
	if [ "$before" != "$after" ]; then
		echo "make generate changed generated code; commit the result"
		diff <(echo "$before") <(echo "$after")
		return 1
	fi
}
step_test() { go test -race -count=1 ./...; }
step_e2e() { go test -tags=e2e -race -count=1 ./test/e2e/...; }
step_lint() { make lint; }
step_spec-lint() { make spec-lint; }
step_spec-diff() { BASE="$BASE" make spec-diff; }
step_deploy-check() { make deploy-check; }
step_license() { ./scripts/license-check.sh; }
step_govulncheck() {
	if command -v govulncheck >/dev/null 2>&1; then
		govulncheck ./...
	else
		go run golang.org/x/vuln/cmd/govulncheck@latest ./...
	fi
}
step_gitleaks() {
	docker run --rm -v "$PWD:/repo:ro" "$GITLEAKS_IMAGE" dir /repo --no-banner --redact || return 1
	docker run --rm -v "$PWD:/repo:ro" "$GITLEAKS_IMAGE" git /repo --log-opts="$BASE..HEAD" --no-banner --redact
}

failed=()
for name in "${STEPS[@]}"; do
	case "$SKIP" in *",$name,"*) printf '%-14s skipped\n' "$name"; continue ;; esac
	if ! declare -F "step_$name" >/dev/null; then
		echo "unknown step $name; the steps are: ${ALL[*]}" >&2
		exit 2
	fi
	log="$CHECK_LOGS/$name.log"
	start=$SECONDS
	if "step_$name" >"$log" 2>&1; then
		printf '%-14s ok    %4ds\n' "$name" $((SECONDS - start))
	else
		printf '%-14s FAIL  %4ds  %s\n' "$name" $((SECONDS - start)) "$log"
		# A Go test failure is its FAIL and file:line lines, and govulncheck's is
		# the name of each vulnerability. Anything else is the end of the log.
		if grep -q -- '--- FAIL' "$log"; then
			grep -E -- '--- FAIL|^\s+\S+_test\.go:[0-9]+:|^panic:' "$log" | head -n "$TAIL" | cut -c1-240 | sed 's/^/    /'
		elif grep -q '^Vulnerability #' "$log"; then
			grep -A1 -E '^Vulnerability #' "$log" | grep -v '^--$' | head -n "$TAIL" | sed 's/^/    /'
		else
			tail -n "$TAIL" "$log" | cut -c1-240 | sed 's/^/    /'
		fi
		failed+=("$name")
	fi
done

if [ ${#failed[@]} -gt 0 ]; then
	echo "failed: ${failed[*]}"
	exit 1
fi
