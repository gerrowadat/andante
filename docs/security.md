# Security

## Reporting a vulnerability

Use
[GitHub private vulnerability reporting](https://github.com/gerrowadat/andante/security/advisories/new),
not a public issue. See [`SECURITY.md`](../SECURITY.md).

## What is scanned

| What | Tool | Workflow | Runs on | Fails the run? |
|---|---|---|---|---|
| Go code reachable from andante | govulncheck | `security.yml` | Pushes to main, PRs, weekly, manual | No, reports only |
| Go modules | Trivy | `security.yml` | Same | No, reports only |
| Container image | Trivy | `security.yml` | Same | Yes, on fixable CRITICAL or HIGH |
| Git history | gitleaks | `security.yml` | Same | Yes, on any finding |
| andante's source | CodeQL | `codeql.yml` | Pushes to main, PRs, weekly | Code scanning reports the findings |
| Dependencies a PR adds | dependency-review | `dependency-review.yml` | PRs | Yes, on high severity or a license outside the allow-list |
| Repository practices | OpenSSF Scorecard | `scorecard.yml` | Pushes to main, weekly, manual | No, reports only |
| Workflow files | zizmor | `ci.yml` | Pushes to main, PRs | Yes |
| Commits being pushed | Secret scanning and push protection | GitHub | Every push | Blocks the push |

Dependabot opens PRs weekly for Go modules, GitHub Actions and Docker base
images. Actions are pinned by commit SHA, and base images by digest, with the
version alongside.

## Where results appear

SARIF from govulncheck, Trivy, CodeQL and Scorecard appears under
**Security → Code scanning**, split by category. Results for a PR appear on the
PR. A PR from a fork does not upload SARIF, because its token cannot write
security events. gitleaks, dependency-review and zizmor report in their job logs.

## Triage

For each alert, do one of these:

- **Fix it.** Update the dependency, change the code, or bump the base image.
- **Dismiss it in the Security tab with a reason**, if it does not apply. Give
  the reason in the dismissal, so the next reader does not have to work it out
  again.

The image gate only fails on findings with a fix available, so a red
`trivy-image` job always has something to act on. Dismissing an alert does not
clear that gate; fix the finding.

A leaked secret is revoked first. Removing it from history comes second, and
does not replace revoking it.
