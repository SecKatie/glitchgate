# Research: Fallback Models

**Feature**: 008-model-fallback
**Date**: 2026-03-11

---

## Decision 1: Where does the fallback config live?

**Decision**: Extend the existing `ModelMapping` struct with a `Fallbacks []string` field. An entry is a *direct* model if it has `Provider`+`UpstreamModel` set; it is a *virtual* model if it has `Fallbacks` set. The two forms are mutually exclusive. No new top-level config section is needed.

**Rationale**: Keeps a single `model_list` array in config. Virtual model names are looked up through the same `FindModel` path as direct models, so no call-site changes outside the config and handler packages.

**Alternatives considered**:
- Separate `model_fallbacks` top-level section — adds complexity and splits the model namespace across two lists; rejected.
- Embedded struct union — idiomatic in Go but more verbose for YAML operators; rejected in favour of flat struct with validation at load time.

---

## Decision 2: Chain resolution — eager vs. lazy

**Decision**: Flatten all virtual model chains into `[]ModelMapping` at `Config.Load` time. `FindModel` returns `[]ModelMapping` (a slice of one for direct models). Resolution and cycle detection happen once at startup, not per request.

**Rationale**: Startup-time flattening is O(N) over the config (tiny). Per-request resolution would add negligible overhead but also adds no benefit since config is immutable at runtime. Eager resolution also makes cycle detection straightforward. SC-002 (no measurable per-request overhead) is trivially satisfied.

**How nesting works**: If virtual model `A` has `fallbacks: [B, C]` and `B` is also a virtual model with `fallbacks: [B1, B2]`, the flattened chain for `A` is `[B1, B2, C]`. This preserves FR-005 semantics: `B`'s full chain is exhausted before `C` is tried.

**Alternatives considered**:
- Lazy recursive resolution per request — more flexible but wastes CPU on every request for a config that never changes; rejected.
- Store nested chains and resolve at dispatch time — preserves nesting metadata but adds a runtime traversal with no benefit; rejected.

---

## Decision 3: `FindModel` return type change

**Decision**: Change `FindModel(name string)` to return `([]ModelMapping, error)`. A direct model returns a slice of one. A virtual model returns the pre-flattened slice. Callers (both `Handler` and `OpenAIHandler`) iterate the slice and stop at the first success.

**Rationale**: Minimal API surface change. Both handlers already have identical model-resolution + dispatch logic, so a single type change propagates cleanly to both.

**Alternatives considered**:
- New `FindModelChain` method alongside existing `FindModel` — avoids breaking callers but leaves two divergent code paths; rejected.
- Return a new `ModelChain` struct — more expressive but unnecessary indirection for a slice; rejected.

---

## Decision 4: Fallback trigger conditions

**Decision**: An attempt is considered failed and triggers fallback when:
1. `prov.SendRequest` returns a non-nil `error` (network-level: timeout, connection refused, etc.)
2. `provResp.StatusCode >= 500` (server error)
3. `provResp.StatusCode == 429` (rate limited)

All other `4xx` responses are returned to the client immediately without fallback.

**Rationale**: Matches FR-006 and FR-007. 429 is provider-side rate limiting, not a request defect. 5xx and network errors indicate provider unavailability. 4xx (other than 429) indicate a problem with the request itself — retrying the same request against a different provider would most likely fail for the same reason.

**Implementation**: A small `isFallbackStatus(code int) bool` helper in the proxy package covers cases 2 and 3.

---

## Decision 5: Streaming fallback boundary

**Decision**: For streaming requests, check `provResp.StatusCode` after `SendRequest` returns but **before** calling `handleStreaming`. If the status triggers fallback, close `provResp.Stream` (to avoid a goroutine/fd leak) and try the next entry. Once `handleStreaming` is called — which immediately calls `w.WriteHeader(http.StatusOK)` in `RelaySSEStream` — no further fallback is possible.

**Rationale**: `SendRequest` returns after upstream response headers are received. The status code is known at this point. `provResp.Stream` is an `io.ReadCloser` that has not yet been read; closing it before forwarding any bytes to the client is safe and leaks nothing to the caller.

---

## Decision 6: Attempt count logging

**Decision**: Add `fallback_attempts INTEGER NOT NULL DEFAULT 1` column to `request_logs` via migration `014`. `RequestLogEntry` gains `FallbackAttempts int64`. The `logRequest` helper gains an `attemptCount int` parameter. Default is `1` (single attempt), so existing log entries and non-virtual requests are unaffected.

**Rationale**: Satisfies FR-012/FR-013. `DEFAULT 1` ensures backward compatibility for all existing rows without a backfill migration.

---

## Decision 7: Cycle detection algorithm

**Decision**: DFS with a "currently in stack" set over the `ModelMapping` entries at `Config.Load` time. Walk each virtual model's flattened dependencies. If a model is encountered that is already in the current DFS path, return an error naming the cycle.

**Rationale**: Config is small (tens of entries at most). DFS is O(V+E) and trivially correct. No external library needed.
