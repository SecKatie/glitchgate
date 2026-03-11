# Data Model: Wildcard Model Routing

**Date**: 2026-03-11
**Feature**: 002-wildcard-model-routing

## Schema Changes

**None.** No database schema changes are required. The existing `request_logs` table already stores `model_requested` and `model_upstream` as TEXT columns, which naturally accommodate wildcard-resolved values.

## Config Model Changes

### ModelMapping (existing struct, no field changes)

| Field           | Type   | Description |
|-----------------|--------|-------------|
| `model_name`    | string | Client-facing model name. Exact match or wildcard pattern ending in `/*` |
| `provider`      | string | Provider name (references a `providers[]` entry) |
| `upstream_model` | string | Upstream model name. **Ignored for wildcard entries** (derived from client request) |

### Wildcard Detection Rule

A `ModelMapping` entry is a wildcard if `model_name` ends with `/*`.

- Prefix = `model_name` with the trailing `/*` removed (e.g., `claude_max/*` → prefix `claude_max`)
- A client model matches if it starts with `prefix/` and has at least one character after the `/`

### FindModel Resolution Order

1. **Exact match pass**: Scan all entries. If `model_name == clientModel`, return immediately.
2. **Wildcard match pass**: Scan entries ending in `/*`. If `clientModel` starts with `prefix/` and suffix is non-empty, return a synthesized `ModelMapping` with:
   - `ModelName` = `clientModel` (original request)
   - `Provider` = from the wildcard entry
   - `UpstreamModel` = suffix after `prefix/`
3. **No match**: Return error.

### Example

Config:
```yaml
model_list:
  - model_name: "claude-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-20250514"
  - model_name: "claude_max/*"
    provider: "claude-max"
```

| Client sends | Match type | Provider | Upstream model |
|-------------|-----------|----------|---------------|
| `claude-sonnet` | Exact | anthropic | `claude-sonnet-4-20250514` |
| `claude_max/claude-sonnet-4-20250514` | Wildcard | claude-max | `claude-sonnet-4-20250514` |
| `claude_max/claude-opus-4-20250514` | Wildcard | claude-max | `claude-opus-4-20250514` |
| `claude_max/` | Wildcard (empty suffix) | — | Error: invalid model |
| `unknown-model` | None | — | Error: model not found |
