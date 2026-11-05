# Phase 1 — Container image and security scanning

Read first: `CLAUDE.md`, `docs/proposals/delivery.md` (sections
"security.yml", "codeql.yml", "dependency-review.yml", "scorecard.yml",
"Supply chain details", "Container image"). Reference:
gerrowadat/nomad-gitops `.github/workflows/security-scan.yml` and
`codeql.yml`, whose comments explain the reasoning; keep that habit of
explaining *why* in workflow comments.

## Goal

Every push to main and every week, the repository scans its source, its
dependencies, its workflows, its history and its container image, and reports
into the GitHub Security tab.

## Do

- `Dockerfile`: builder on `--platform=$BUILDPLATFORM`, `CGO_ENABLED=0`,
  `-trimpath`, ldflags from build args; runtime
  `gcr.io/distroless/static-debian12:nonroot`; both binaries copied in;
  `andante-server` entrypoint; OCI labels (title, source, licenses, version,
  revision, created). Add `import _ "time/tzdata"` to `cmd/andante-server`.
- Add the docker ecosystem to `.github/dependabot.yml`, same schedule and
  grouping as the others.
- `make docker` (local single-arch build) and `make docker-scan` (Trivy, if
  installed).
- `.github/workflows/security.yml`: `govulncheck` (SARIF), `trivy-fs`
  (report only), `trivy-image` (fail on fixable CRITICAL/HIGH), `gitleaks`.
  Triggers: push to main, pull requests, weekly schedule (pick an odd minute),
  manual. The `pull_request` trigger is permanent: it is cheap and useful on
  PRs, and GitHub only dispatches a workflow once its file is on the default
  branch, so it is how this workflow proves itself before merge.
- `.github/workflows/codeql.yml`: Go, push/PR/weekly.
- `.github/workflows/dependency-review.yml`: PRs; fail on high-severity
  vulnerabilities and on licenses outside an allow-list compatible with
  Apache-2.0.
- `.github/workflows/scorecard.yml`: OpenSSF Scorecard, publish results,
  SARIF upload. Add the badge to `README.md`. Give it a temporary
  `pull_request` trigger with `publish_results` off on that event, so it can
  run on this PR, and remove the trigger in the last commit before merge.
- Pin every action in every workflow (including `ci.yml`) by full commit SHA
  with `# vX.Y.Z` comments. Make sure Dependabot's github-actions config keeps
  them current.
- Audit every workflow for minimal per-job `permissions:`, no
  `pull_request_target`, inputs via `env:` only. zizmor must pass.
- `docs/security.md`: what is scanned, where results appear, how to triage
  (fix, or dismiss in the Security tab with a reason), and how to report a
  vulnerability.

## Out of scope

Publishing images or releases (phase 2).

## Acceptance criteria

- [ ] All new workflows green on the PR.
- [ ] After merge (human-run): a `workflow_dispatch` dry run of `security.yml` and `scorecard.yml` on main is green, and SARIF from govulncheck, Trivy, CodeQL and Scorecard is visible under Security → Code scanning.
- [ ] `docker build .` produces an image where `andante-server --version` works and runs as non-root.
- [ ] zizmor reports nothing at default severity.

## Finish

Branch `phase-1-security`. PR. Stop.
