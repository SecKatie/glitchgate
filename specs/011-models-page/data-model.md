# Data Model: Models Page

**Branch**: `011-models-page` | **Date**: 2026-03-13

## Overview

This feature is read-only — no schema changes. It combines two existing data sources:
1. **Config** (`config.ModelMapping`, `config.ProviderConfig`) — loaded at startup, passed to handlers
2. **Request Logs** (`request_logs` table) — queried for per-model usage aggregates

One new store query is added. One new view type is introduced in the `web` package.

---

## New Store Type: ModelUsageSummary

Lives in `internal/store/store.go`.

```go
// ModelUsageSummary holds aggregated usage statistics for a single model name.
type ModelUsageSummary struct {
    RequestCount  int64
    InputTokens   int64
    OutputTokens  int64
    TotalCostUSD  float64
}
```

### New Store Interface Method

```go
// GetModelUsageSummary returns aggregated usage stats for a given model_requested value.
// Returns a zero-value summary (not an error) if no logs exist for the model.
GetModelUsageSummary(ctx context.Context, modelName string) (*ModelUsageSummary, error)
```

### SQL Query (sqlite.go implementation)

```sql
SELECT
    COUNT(*)                          AS request_count,
    COALESCE(SUM(input_tokens), 0)    AS input_tokens,
    COALESCE(SUM(output_tokens), 0)   AS output_tokens,
    COALESCE(SUM(estimated_cost_usd), 0) AS total_cost_usd
FROM request_logs
WHERE model_requested = ?
```

---

## New Web View Types

Lives in `internal/web/model_handlers.go`.

```go
// ModelListItem is a row on the Models list page.
type ModelListItem struct {
    ModelName     string          // client-facing name from config
    ProviderName  string          // empty for virtual models
    ProviderType  string          // e.g. "anthropic", "openai", "github_copilot"
    IsVirtual     bool            // true if fallback-chain model
    IsWildcard    bool            // true if model_name ends in "/*"
    Fallbacks     []string        // populated for virtual models
    Pricing       *pricing.Entry  // nil if no pricing known
    HasPricing    bool
    EncodedName   string          // url.PathEscape(ModelName) for use in href
}

// ModelDetailView is the data for the model detail page.
type ModelDetailView struct {
    ActiveTab     string
    ModelName     string
    ProviderName  string
    ProviderType  string
    IsVirtual     bool
    IsWildcard    bool
    Fallbacks     []string
    Pricing       *pricing.Entry
    HasPricing    bool
    Usage         *store.ModelUsageSummary
    CurlExample   string          // pre-formatted curl command string
    UpstreamModel string          // empty for virtual/wildcard
}
```

---

## Changes to Existing Types

### `internal/web/handlers.go` — `Handlers` struct

Add two new fields:

```go
type Handlers struct {
    // ... existing fields ...
    modelList []config.ModelMapping
    providers []config.ProviderConfig
}
```

### `NewHandlers` signature update

```go
func NewHandlers(
    s store.Store,
    sessions *auth.UISessionStore,
    masterKey string,
    calc *pricing.Calculator,
    tmpl *TemplateSet,
    oidcProvider OIDCProvider,
    modelList []config.ModelMapping,
    providers []config.ProviderConfig,
) *Handlers
```

---

## Validation Rules

- `ModelName` must be non-empty (enforced by config.Load)
- `EncodedName` is derived from `ModelName` using `url.PathEscape` — never stored
- `CurlExample` is a static string template populated at handler time — never stored
- `ModelUsageSummary` fields are all non-negative integers/floats; zero values are valid

---

## Entity Relationships

```text
config.ModelMapping (1) ──── (0..1) config.ProviderConfig
                        └─── (0..1) pricing.Entry   [via Calculator.Lookup]
                        └─── (0..1) ModelUsageSummary [via GetModelUsageSummary]
```

Virtual models have no direct ProviderConfig or pricing entry. Their fallback chain members each have their own ProviderConfig + pricing.
