# Research: Cache Token Usage Logging

**Feature**: 003-cache-token-logging
**Date**: 2026-03-11

---

## Decision 1: Canonical cache token field names

**Decision**: Use Anthropic's published field names verbatim throughout all internal types — `cache_creation_input_tokens` and `cache_read_input_tokens`.

**Rationale**: The spec explicitly includes the live Anthropic API payload. Using matching field names eliminates any translation layer between the upstream JSON and internal structs, reduces cognitive overhead for future contributors, and aligns with the constitution's correctness principle ("faithfully translate without data loss or semantic drift").

**Alternatives considered**:
- Generic names like `cache_write_tokens` / `cache_read_tokens` — shorter but diverges from the upstream schema without benefit.

---

## Decision 2: Cache pricing rates

**Decision**: Apply Anthropic's published per-model cache pricing (as of 2026-03):
- Cache write (`cache_creation_input_tokens`): **25% above** the model's standard input rate
- Cache read (`cache_read_input_tokens`): **10%** of the model's standard input rate

Computed concrete rates for default models:

| Model | Standard Input $/1M | Cache Write $/1M | Cache Read $/1M |
|---|---|---|---|
| claude-sonnet-4-20250514 | $3.00 | $3.75 | $0.30 |
| claude-opus-4-20250514 | $15.00 | $18.75 | $1.50 |
| claude-haiku-4-20250514 | $0.80 | $1.00 | $0.08 |

**Rationale**: Using the published rate relationships (×1.25 and ×0.10) rather than independently hard-coded values makes it easy to verify correctness and update if Anthropic changes pricing.

**Alternatives considered**:
- Hard-coding flat values independently — error-prone, harder to audit.
- Not adding cache pricing and treating cache tokens as zero cost — this satisfies FR-006 for unknown models but must not apply to known models.

---

## Decision 3: Where cache tokens are extracted in the streaming path

**Decision**: Extract `cache_creation_input_tokens` and `cache_read_input_tokens` only from the `message_start` SSE event. The `message_delta` event's `usage` object carries only `output_tokens` and does not contain cache token fields.

**Rationale**: The Anthropic streaming spec places cache token counts in the initial `message_start` event alongside regular `input_tokens`. The `extractTokens` function in `internal/proxy/stream.go` already handles this distinction. Adding cache fields to the `message_start` branch requires no new event types.

**Alternatives considered**:
- Scanning all event types for cache fields — unnecessary; the API does not put them elsewhere.

---

## Decision 4: Query implementation layer

**Decision**: Update `internal/store/sqlite.go` (the hand-written SQL implementation). Also update `queries/*.sql` for documentation consistency and future sqlc compatibility, but do not run `sqlc generate` as part of this feature — the build does not currently depend on generated output.

**Rationale**: There are no generated sqlc files in `internal/store/` (no `db.go`, no `query.sql.go`). The `sqlc.yaml` is configured but generation has not been run. The actual executable queries live in `sqlite.go`. Updating both files keeps them in sync without introducing a build step that isn't yet established.

**Alternatives considered**:
- Running `sqlc generate` and deleting hand-written queries — would be a larger refactor outside this feature's scope.

---

## Decision 5: `provider.Response` and `StreamResult` carry cache counts independently

**Decision**: Add `CacheCreationInputTokens int64` and `CacheReadInputTokens int64` to both `provider.Response` (non-streaming path, populated by the Anthropic client) and `StreamResult` (streaming path, populated by `extractTokens`). The handler reads from whichever is applicable and passes both values to `logRequest`.

**Rationale**: The two paths already handle `InputTokens`/`OutputTokens` this way. Extending both structs with the same fields maintains symmetry and keeps the handler unaware of whether the values came from streaming or non-streaming.

**Alternatives considered**:
- A single shared "usage" struct extracted at parse time — adds a layer of indirection with no benefit at this scale.

---

## Decision 6: `logRequest` signature extension

**Decision**: Add `cacheCreationTokens int64` and `cacheReadTokens int64` as two additional parameters to the existing `logRequest` helper in `handler.go`. All call sites are in the same file.

**Rationale**: The existing pattern uses explicit positional parameters. There are only three call sites, all in `handler.go`. Introducing a struct purely for this change would be premature; the constitution discourages over-engineering for single-file changes.

**Alternatives considered**:
- A `logParams` struct — better if the parameter count were to grow further; can be done as a follow-up refactor.
