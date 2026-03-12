# Data Model: Fallback Models

**Feature**: 008-model-fallback
**Date**: 2026-03-11

---

## Config: ModelMapping (modified)

The existing `ModelMapping` struct gains a `Fallbacks` field. The two forms are mutually exclusive.

### Fields

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `model_name` | string | yes | Client-facing name; must be unique across all `model_list` entries |
| `provider` | string | direct only | Name of a configured provider; empty for virtual entries |
| `upstream_model` | string | direct only | Model name sent to the upstream provider; empty for virtual entries |
| `fallbacks` | []string | virtual only | Ordered list of other `model_name` values to try; empty for direct entries |

### Validation rules (enforced at `Config.Load`)

- An entry MUST have either (`provider` AND `upstream_model`) OR (`fallbacks`), not both and not neither.
- Every name in `fallbacks` MUST exist as a `model_name` elsewhere in `model_list`.
- No circular references among virtual entries (DFS cycle detection).
- `model_name` values MUST be unique within `model_list`.

### YAML example

```yaml
model_list:
  # Direct entry (unchanged form)
  - model_name: "claude-haiku"
    provider: "anthropic-primary"
    upstream_model: "claude-3-5-haiku-20241022"

  # Virtual entry — single indirection (provider-swap use case)
  - model_name: "smart"
    fallbacks:
      - "smart-a"

  # Virtual entry — fallback chain
  - model_name: "smart-resilient"
    fallbacks:
      - "smart-a"
      - "smart-b"

  # Virtual referencing another virtual
  - model_name: "smart-ultra-resilient"
    fallbacks:
      - "smart-resilient"   # itself a virtual: tries smart-a, smart-b first
      - "claude-haiku"      # final backstop

  - model_name: "smart-a"
    provider: "anthropic-primary"
    upstream_model: "claude-3-5-sonnet-20241022"

  - model_name: "smart-b"
    provider: "anthropic-secondary"
    upstream_model: "claude-3-5-sonnet-20241022"
```

### Pre-computed chain examples (after Load)

| Virtual name | Flattened chain |
|---|---|
| `smart` | `[smart-a]` |
| `smart-resilient` | `[smart-a, smart-b]` |
| `smart-ultra-resilient` | `[smart-a, smart-b, claude-haiku]` |

---

## Store: `request_logs` table (migration 014)

New column added to the existing table.

| Column | Type | Default | Notes |
|--------|------|---------|-------|
| `fallback_attempts` | INTEGER NOT NULL | 1 | Number of provider dispatches made for this request. 1 = first choice succeeded or non-virtual model. |

### Migration SQL

```sql
-- +goose Up
ALTER TABLE request_logs ADD COLUMN fallback_attempts INTEGER NOT NULL DEFAULT 1;

-- +goose Down
-- SQLite does not support DROP COLUMN in older versions; down migration is a no-op.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
```

---

## Go types (modified)

### `internal/config.ModelMapping`

```go
type ModelMapping struct {
    ModelName     string   `mapstructure:"model_name"     yaml:"model_name"`
    Provider      string   `mapstructure:"provider"       yaml:"provider"`
    UpstreamModel string   `mapstructure:"upstream_model" yaml:"upstream_model"`
    Fallbacks     []string `mapstructure:"fallbacks"      yaml:"fallbacks"`
}
```

### `internal/config.Config` (internal pre-computed field)

```go
// resolvedChains is populated at Load time. Key = model_name.
// Direct models map to a slice of one. Virtual models map to a flattened slice.
resolvedChains map[string][]ModelMapping
```

### `FindModel` new signature

```go
// FindModel resolves a client-facing model name to an ordered slice of
// concrete provider-model mappings to attempt. A direct model returns a
// slice of one. A virtual model returns its pre-flattened fallback chain.
func (c *Config) FindModel(modelName string) ([]ModelMapping, error)
```

### `internal/store.RequestLogEntry` (modified)

```go
type RequestLogEntry struct {
    // ... existing fields unchanged ...
    FallbackAttempts int64  // 1 = single attempt (default)
}
```

### `internal/store.RequestLogSummary` (modified)

```go
type RequestLogSummary struct {
    // ... existing fields unchanged ...
    FallbackAttempts int64
}
```

---

## Fallback trigger predicate

```go
// isFallbackStatus returns true when an HTTP status code from an upstream
// provider should trigger fallback to the next chain entry.
// Triggers: 5xx server errors and 429 rate limiting.
// Does NOT trigger: other 4xx client errors.
func isFallbackStatus(code int) bool {
    return code >= 500 || code == 429
}
```
