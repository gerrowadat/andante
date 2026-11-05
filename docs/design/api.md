# Design: API contract

Why the admin API's contract is the way it is. How to use it is in
[`api.md`](../api.md). The rest of the API's design is in the
[architecture proposal](../proposals/architecture.md) until it graduates.

## OpenAPI 3.1

The spec is OpenAPI 3.1.0, not the 3.0.3 fallback. Before writing it, a small spec
using `type: [string, "null"]`, discriminated `oneOf` and merge patch was run
through everything that has to read it: oapi-codegen v2.8.2, kin-openapi's request
validator, Spectral and oasdiff. All of them handled it.

One difference remains: on 3.1 kin-openapi validates with a JSON Schema 2020-12
library, whose error text names its internal schema URL. So field errors are built
from that library's structured error tree, not from the message.

## Decisions

- **`strategy` is a property of `config`.** A `oneOf` discriminator must be a
  property of the object it discriminates, so a sibling `strategy` field would not
  work. The database keeps its own `strategy` column as well, for the crawler.
- **Authentication comes from the spec.** The request validator calls the
  authenticator for any operation with a `security` requirement, and the document
  sets one globally. A new operation is therefore authenticated unless it says
  `security: []`, so forgetting fails closed.
- **The validator runs before routing.** It knows the whole contract, including
  operations that are not built, so it answers 404 and 405 itself and the mux
  never has to.
- **`PATCH` is RFC 7396 merge patch.** It is the open standard, and it can tell a
  field left out from a field set to `null`.
- **The last active API token cannot be revoked.** Bootstrap only runs on an empty
  table, and revoked rows stay, so revoking it would lock the API out for good.
- **Request validation is 400, config validation is 422.** A 400 means the request
  does not match the contract. A 422 means it does, but the strategy refuses it.
- **Models live in `pkg/client`.** The server's generated code imports them, so
  server and clients cannot disagree about a type.
- **Metrics label by route pattern, with `unmatched` for the rest.** The raw path
  would let any client create unbounded label values.
- **`GET /api/v1/openapi.json` is in the contract.** It is an operation like any
  other, so the CLI and its operationId check cover it.

## Where this diverged from the architecture proposal

- `POST /api/v1/exports/opml` and `POST /api/v1/imports/opml` are `text/x-opml`.
- `last_used_at` is updated at most once a minute by a fixed interval in the query,
  which is the one timing value here that is not a flag.
