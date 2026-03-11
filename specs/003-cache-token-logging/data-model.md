# Data Model: Cache Token Usage Logging

**Feature**: 003-cache-token-logging
**Date**: 2026-03-11

---

## Schema Change

### New migration: `003_add_cache_tokens.sql`

Adds two non-nullable integer columns to `request_logs`, defaulting to `0`.
Existing rows automatically receive `0` — no data migration required.

```sql
ALTER TABLE request_logs ADD COLUMN cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_logs ADD COLUMN cache_read_input_tokens INTEGER NOT NULL DEFAULT 0;
```

---

## Struct Changes

### `internal/provider/anthropic/types.go`

**`Usage`** — extend with cache fields (appear in non-streaming response and `message_start` SSE event):

```go
type Usage struct {
    InputTokens                int64 `json:"input_tokens"`
    OutputTokens               int64 `json:"output_tokens"`
    CacheCreationInputTokens   int64 `json:"cache_creation_input_tokens"`
    CacheReadInputTokens       int64 `json:"cache_read_input_tokens"`
}
```

`DeltaUsage` — no change (carries only `output_tokens` per Anthropic streaming spec).

---

### `internal/provider/provider.go`

**`Response`** — extend with cache token counts, parallel to existing fields:

```go
type Response struct {
    StatusCode                 int
    Headers                    http.Header
    Body                       []byte
    Stream                     io.ReadCloser
    InputTokens                int64
    OutputTokens               int64
    CacheCreationInputTokens   int64
    CacheReadInputTokens       int64
}
```

---

### `internal/proxy/stream.go`

**`StreamResult`** — extend with cache token counts:

```go
type StreamResult struct {
    Body                       []byte
    InputTokens                int64
    OutputTokens               int64
    CacheCreationInputTokens   int64
    CacheReadInputTokens       int64
}
```

---

### `internal/pricing/calculator.go`

**`PricingEntry`** — add cache rate fields:

```go
type PricingEntry struct {
    InputPerMillion            float64
    OutputPerMillion           float64
    CacheWritePerMillion       float64  // cache_creation_input_tokens rate
    CacheReadPerMillion        float64  // cache_read_input_tokens rate
}
```

`Calculate` signature gains two parameters:

```go
func (c *Calculator) Calculate(
    upstreamModel string,
    inputTokens, outputTokens,
    cacheCreationTokens, cacheReadTokens int64,
) *float64
```

---

### `internal/store/store.go`

**`RequestLogEntry`** — new fields:

```go
CacheCreationInputTokens   int64
CacheReadInputTokens       int64
```

**`RequestLogSummary`** — new fields (same):

```go
CacheCreationInputTokens   int64
CacheReadInputTokens       int64
```

**`CostSummary`** — new fields:

```go
TotalCacheCreationTokens   int64
TotalCacheReadTokens       int64
```

**`CostBreakdownEntry`** — new fields:

```go
CacheCreationTokens   int64
CacheReadTokens       int64
```

---

## Data Flow

```
Upstream API response (JSON)
  └── Usage.CacheCreationInputTokens
  └── Usage.CacheReadInputTokens
        │
        ├── Non-streaming path
        │     anthropic/client.go → provider.Response.CacheCreationInputTokens
        │                         → provider.Response.CacheReadInputTokens
        │     handler.go (handleNonStreaming) → logRequest(cacheCreation, cacheRead)
        │
        └── Streaming path (message_start SSE event)
              proxy/stream.go extractTokens() → StreamResult.CacheCreationInputTokens
                                              → StreamResult.CacheReadInputTokens
              handler.go (handleStreaming) → logRequest(cacheCreation, cacheRead)
                    │
                    ▼
              store.RequestLogEntry.CacheCreationInputTokens
              store.RequestLogEntry.CacheReadInputTokens
                    │
                    ▼
              request_logs table (cache_creation_input_tokens, cache_read_input_tokens)
                    │
                    ▼
              GetCostSummary → CostSummary.TotalCacheCreationTokens / TotalCacheReadTokens
              GetCostBreakdown → CostBreakdownEntry.CacheCreationTokens / CacheReadTokens
```

---

## Pricing Rates (defaults.go)

| Model | CacheWritePerMillion | CacheReadPerMillion |
|---|---|---|
| claude-sonnet-4-20250514 | 3.75 | 0.30 |
| claude-opus-4-20250514 | 18.75 | 1.50 |
| claude-haiku-4-20250514 | 1.00 | 0.08 |
