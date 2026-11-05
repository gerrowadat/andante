# Phase 4 — API contract

Read first: `CLAUDE.md`, `docs/philosophy.md` ("The API is the only way in",
"Open standards first"), `docs/proposals/architecture.md` ("Admin API (v1)").

## Goal

The whole v1 admin API is described in `api/openapi.yaml`; server stubs and a
client are generated from it; CI protects it; API tokens and sources CRUD are
implemented for real. Every other endpoint returns 501 Problem Details until
its phase.

This may be split into two PRs: (a) spec, generation, middleware and CI; (b)
API tokens and sources handlers.

## Do

- `api/openapi.yaml`: OpenAPI 3.1 if the chosen oapi-codegen version supports
  everything this spec uses; otherwise 3.0.3 (record which and why in
  `docs/design/api.md`). All endpoints from the architecture doc, including
  `GET /api/v1/openapi.json`. Shared
  components: `Problem` (RFC 9457), cursor pagination parameters and
  `next` links, bearer security scheme, RFC 3339 `date-time` fields. Every
  operation has an `operationId`, a summary, and examples for request and
  response bodies. Source `config` is a `oneOf` discriminated by `strategy`.
- Code generation with oapi-codegen v2: `std-http-server` + `strict-server`
  into `internal/api/gen/`, `client` + models into `pkg/client/`. Wire it into
  `make generate`. Pin the generator version with a `tool` directive in
  `go.mod`.
- Middleware: bearer auth against `api_tokens` (constant-time compare of
  hashes, `last_used_at` updated at most once a minute), RFC 9457 error
  rendering for every error path including 404/405 from the mux and
  request-validation failures, request ID header, metrics
  (`andante_api_requests_total{route,status}` using the route pattern, never
  the raw path).
- Serve the spec at `GET /api/v1/openapi.json` (admin listener, no auth).
- Handlers: `api-tokens` (list/create/revoke; raw token returned only on
  create), `sources` (list/create/get/patch/delete with per-strategy config
  validation through a registry that phase 5 fills in — for now accept only
  `crawl` with `start_url` and optional selector overrides), `GET
  /sources/{id}/entries` (paginated, empty until phase 5).
- CI `contracts` job: Spectral lint of the spec (ruleset in `.spectral.yaml`,
  extending `spectral:oas`); `make generate` + diff; on PRs, `oasdiff breaking`
  between the base branch's spec and this one, failing on ERR.
- `docs/api.md`: auth, errors, pagination, versioning policy (additive within
  v1; breaking means v2), where the spec lives, how to generate a client in
  another language.

## Out of scope

Behaviour behind probes, crawls, reads, tokens, OPML (later phases). The CLI
(phase 8) — but `pkg/client` must be usable, proven by tests that drive the
handlers through it.

## Acceptance criteria

- [ ] Spectral clean; generated code current; oasdiff wired (prove it fails on a deliberately breaking change in a throwaway commit, then drop it).
- [ ] Handler tests run through `pkg/client` against an `httptest` server and a real test database.
- [ ] Every error response, including auth failures and unknown routes, is `application/problem+json`.
- [ ] Unimplemented endpoints return 501 Problem Details with a `type` URI that says so.

## Finish

Branch `phase-4-api` (or `phase-4a-…`/`phase-4b-…`). PR. Stop.
