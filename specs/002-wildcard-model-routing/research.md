# Research: Wildcard Model Routing

**Date**: 2026-03-11
**Feature**: 002-wildcard-model-routing

## Decision 1: Where to implement wildcard matching

**Decision**: Modify `config.FindModel()` to handle wildcards internally. Return a synthesized `ModelMapping` with the resolved upstream model name.

**Rationale**: `FindModel()` is the single entry point for model resolution, called by both the Anthropic handler (`handler.go:69`) and OpenAI handler (`openai_handler.go:71`). Changing it once gives wildcard support to both paths with zero handler changes. The returned `ModelMapping` struct already carries `Provider`, `UpstreamModel`, and `ModelName` — for wildcard matches, `UpstreamModel` is set to the stripped suffix and `ModelName` is set to the original client request.

**Alternatives considered**:
- Add a separate `FindWildcardModel()` method and call it in both handlers — rejected because it duplicates the call site logic and violates the single-responsibility of `FindModel()`.
- Pre-expand wildcards at config load time into a lookup map — rejected because the whole point is to avoid enumerating models; the wildcard set is unbounded.

## Decision 2: Matching algorithm and precedence

**Decision**: Two-pass linear scan: (1) exact match pass over all entries, (2) wildcard match pass over entries ending in `/*`. First match in each pass wins.

**Rationale**: Exact matches must always take priority (FR-003). A two-pass approach is the simplest way to guarantee this without reordering the config or building an index. With <20 entries typical, performance is negligible.

**Alternatives considered**:
- Single-pass with "exact wins on tie" — more complex to implement correctly when wildcards can appear anywhere in the list. Rejected for complexity.
- Build a trie/prefix tree at config load — over-engineering for <20 entries. Rejected per constitution principle II.

## Decision 3: Wildcard detection

**Decision**: A model_list entry is a wildcard if and only if its `model_name` field ends with `/*`. The prefix is everything before `/*`.

**Rationale**: Matches the user's example (`claude_max/*`). The `/*` suffix is unambiguous and won't conflict with real model names (which use hyphens and dots, not trailing slashes). No config schema changes needed — the existing `model_name` string field carries the pattern.

**Alternatives considered**:
- A separate boolean `wildcard: true` field in config — rejected because it requires schema changes and is redundant with the `/*` suffix convention.
- Glob-style matching (`claude_max*` without slash) — rejected because it's ambiguous (does `claude_max_v2` match?). The `/` delimiter makes the boundary explicit.

## Decision 4: What FindModel returns for wildcard matches

**Decision**: Return a `*ModelMapping` with:
- `ModelName` = original client request (e.g., `claude_max/claude-sonnet-4-20250514`)
- `Provider` = from the wildcard config entry
- `UpstreamModel` = stripped suffix (e.g., `claude-sonnet-4-20250514`)

**Rationale**: Callers already use `modelMapping.UpstreamModel` for the upstream request and `modelMapping.ModelName` (or the original `reqBody.Model`) for logging. Setting `UpstreamModel` to the resolved suffix means both handlers work without changes. The Anthropic handler uses `reqBody.Model` for `modelRequested` logging; the OpenAI handler uses `modelMapping.ModelName`. Both paths are correct.

**Alternatives considered**:
- Return a new `ResolvedModel` struct instead of `ModelMapping` — rejected because it would require changing both handler call sites for no benefit.

## Decision 5: Empty suffix handling

**Decision**: If the suffix after stripping the prefix is empty (e.g., `claude_max/`), return an error from `FindModel()`.

**Rationale**: An empty model name is never valid for an upstream provider. Failing early with a clear error is better than forwarding a bad request upstream.
