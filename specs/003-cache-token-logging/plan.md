# Implementation Plan: Cache Token Usage Logging

**Branch**: `003-cache-token-logging` | **Date**: 2026-03-11 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/003-cache-token-logging/spec.md`

## Summary

Extend the proxy's request/response logging to capture `cache_creation_input_tokens` and `cache_read_input_tokens` from Anthropic API usage payloads (both streaming and non-streaming). Store them in the database, incorporate cache-specific pricing into cost estimates, and surface the totals in the cost dashboard API and web UI.

## Technical Context

**Language/Version**: Go 1.24+
**Primary Dependencies**: chi/v5 (HTTP router), go-resty/v3 (upstream calls), modernc.org/sqlite (pure-Go SQLite), goose/v3 (migrations), testify/require (tests)
**Storage**: SQLite — one new migration (`003_add_cache_tokens.sql`) adding two columns to `request_logs`
**Testing**: `go test -race ./...` with table-driven tests and testify/require
**Target Platform**: Linux server (single static binary, CGO_ENABLED=0)
**Project Type**: Web service / reverse proxy
**Performance Goals**: Negligible hot-path impact — cache field extraction reuses the existing JSON parse already performed in `extractTokens` and `client.go`
**Constraints**: All struct changes must be backward-compatible (zero-value defaults); migration must not touch existing row data
**Scale/Scope**: ~12 files touched, no new packages, no new dependencies (one new template helper function)

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|---|---|---|
| I. Speed Above All | ✅ PASS | Cache fields extracted from the same JSON parse already in flight; two int64 reads add no measurable latency |
| II. Efficient Use of Resources | ✅ PASS | Two int64 fields per struct (16 bytes); no new allocations on the hot path |
| III. Clean Abstractions | ✅ PASS | Provider-specific types stay in `anthropic/types.go`; shared transport types in `provider/provider.go`; store types in `store/store.go` — follows existing layering |
| IV. Correctness and Compatibility | ✅ PASS | DB migration uses `ALTER TABLE … DEFAULT 0` preserving all existing rows; all new fields have zero-value defaults |
| V. Security by Default | ✅ PASS | No new endpoints, no new auth surface, no secrets involved |

**Post-design re-check**: All principles still satisfied. No violations to justify.

## Project Structure

### Documentation (this feature)

```text
specs/003-cache-token-logging/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
└── tasks.md             # Phase 2 output (/speckit.tasks — not created here)
```

### Source Code (files touched)

```text
internal/
├── provider/
│   ├── provider.go                          # +2 fields on Response
│   └── anthropic/
│       ├── types.go                         # +2 fields on Usage
│       └── client.go                        # extract cache tokens (non-streaming)
├── proxy/
│   ├── stream.go                            # +2 fields on StreamResult; extract in extractTokens
│   └── handler.go                           # pass cache tokens through logRequest
├── pricing/
│   ├── calculator.go                        # +2 fields on PricingEntry; update Calculate sig
│   └── defaults.go                          # add CacheWritePerMillion/CacheReadPerMillion
├── store/
│   ├── store.go                             # +2 fields on RequestLogEntry, RequestLogSummary,
│   │                                        #   CostSummary, CostBreakdownEntry
│   ├── sqlite.go                            # update all SQL: insert, list, get, cost queries
│   └── migrations/
│       └── 003_add_cache_tokens.sql         # NEW — ALTER TABLE to add 2 columns
└── web/
    ├── cost_handlers.go                     # +2 fields on costSummaryResponse,
    │                                        #   costBreakdownEntryJSON; update mapping
    └── templates/
        ├── logs.html                        # "In Tokens" column = sum of all 3 input fields
        ├── log_detail.html                  # Input / Cache Write / Cache Read + Total In rows
        └── costs.html                       # cache write/read totals in summary section

queries/
├── request_logs.sql                         # update InsertRequestLog, ListRequestLogs, GetRequestLog
└── costs.sql                                # update GetTotalCost, GetCostByModel, GetCostByKey
```

**Structure Decision**: Single-project layout. This feature is a vertical slice through existing packages — no new packages or binaries. All changes follow the existing file-per-concern structure.

## Phase 0: Research

All decisions captured in [research.md](research.md). Summary:

1. Use Anthropic's exact field names (`cache_creation_input_tokens`, `cache_read_input_tokens`) verbatim in all internal types.
2. Cache write rate = standard input × 1.25; cache read rate = standard input × 0.10.
3. Cache tokens appear only in `message_start` SSE events (not `message_delta`) — extend only the existing `message_start` branch in `extractTokens`.
4. The `queries/*.sql` files are updated for documentation/future-sqlc consistency but `sqlite.go` is the live implementation.
5. `provider.Response` and `StreamResult` each get two new fields, mirroring the existing `InputTokens`/`OutputTokens` pattern.
6. `logRequest` in `handler.go` gets two additional positional parameters.

## Phase 1: Design

### Step 1 — DB migration (`internal/store/migrations/003_add_cache_tokens.sql`)

```sql
-- +goose Up
ALTER TABLE request_logs
    ADD COLUMN cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_logs
    ADD COLUMN cache_read_input_tokens INTEGER NOT NULL DEFAULT 0;

-- +goose Down
-- SQLite does not support DROP COLUMN in all versions; migration is intentionally irreversible.
-- To roll back, recreate the table without these columns.
```

### Step 2 — Anthropic types (`internal/provider/anthropic/types.go`)

Add `CacheCreationInputTokens` and `CacheReadInputTokens` to `Usage`. `DeltaUsage` is unchanged.

### Step 3 — Provider response (`internal/provider/provider.go`)

Add `CacheCreationInputTokens int64` and `CacheReadInputTokens int64` to `Response`.

### Step 4 — Anthropic client (`internal/provider/anthropic/client.go`)

In `SendRequest`, after populating `InputTokens` and `OutputTokens`, also populate the two new fields from `msgResp.Usage`.

### Step 5 — Stream extraction (`internal/proxy/stream.go`)

- Add `CacheCreationInputTokens` and `CacheReadInputTokens` to `StreamResult`.
- In `extractTokens`, in the `message_start` branch, read the new fields from `envelope.Message.Usage` and write to the new pointer params.
- Update `RelaySSEStream` to initialise, thread, and return the new fields.

### Step 6 — Handler (`internal/proxy/handler.go`)

- Add `cacheCreationTokens, cacheReadTokens int64` to `logRequest` signature.
- In `handleNonStreaming`: pass `resp.CacheCreationInputTokens, resp.CacheReadInputTokens`.
- In `handleStreaming`: pass `result.CacheCreationInputTokens, result.CacheReadInputTokens`.
- In the error path: pass `0, 0`.
- In `logRequest` body: populate the new fields on `RequestLogEntry`.
- Pass cache tokens through to `calculator.Calculate`.

### Step 7 — Pricing (`internal/pricing/calculator.go` + `defaults.go`)

- Add `CacheWritePerMillion float64` and `CacheReadPerMillion float64` to `PricingEntry`.
- Update `Calculate` signature to accept `cacheCreationTokens, cacheReadTokens int64`.
- Include cache cost in the calculation: `float64(cacheCreationTokens)*entry.CacheWritePerMillion + float64(cacheReadTokens)*entry.CacheReadPerMillion`.
- If `CacheWritePerMillion == 0` (model in table but rates not set), emit `log.Printf("WARNING: cache pricing not configured for model %s", upstreamModel)`.
- Update `DefaultPricing` in `defaults.go` with the three models' cache rates.

### Step 8 — Store types (`internal/store/store.go`)

- `RequestLogEntry`: add `CacheCreationInputTokens int64`, `CacheReadInputTokens int64`.
- `RequestLogSummary`: add the same two fields.
- `CostSummary`: add `TotalCacheCreationTokens int64`, `TotalCacheReadTokens int64`.
- `CostBreakdownEntry`: add `CacheCreationTokens int64`, `CacheReadTokens int64`.

### Step 9 — Store implementation (`internal/store/sqlite.go`)

- `InsertRequestLog`: add two columns and two args to the INSERT.
- `ListRequestLogs`: add `rl.cache_creation_input_tokens, rl.cache_read_input_tokens` to SELECT and Scan.
- `GetRequestLog`: same additions to SELECT and Scan.
- `GetCostSummary`: add `COALESCE(SUM(cache_creation_input_tokens), 0)` and `COALESCE(SUM(cache_read_input_tokens), 0)` to SELECT; Scan into new fields.
- `GetCostBreakdown`: add same aggregations to both `key` and `model` branches; Scan into new fields.

### Step 10 — Reference SQL (`queries/request_logs.sql`, `queries/costs.sql`)

Mirror all changes from Step 9 into the sqlc-format query files for documentation consistency.

### Step 11 — Web handler (`internal/web/cost_handlers.go`)

- `costSummaryResponse`: add `TotalCacheCreationTokens int64 \`json:"total_cache_creation_tokens"\`` and `TotalCacheReadTokens int64 \`json:"total_cache_read_tokens"\``.
- `costBreakdownEntryJSON`: add `CacheCreationTokens int64 \`json:"cache_creation_tokens"\`` and `CacheReadTokens int64 \`json:"cache_read_tokens"\``.
- In `CostSummaryHandler`: populate new summary fields from `summary.TotalCacheCreationTokens` / `TotalCacheReadTokens`; populate new breakdown fields from each `CostBreakdownEntry`.
- `CostsPageHandler` and `CostSummaryFragmentHandler`: the `summary` object passed to templates already carries the new fields — no handler changes needed beyond the struct additions.

### Step 12 — Logs list template (`internal/web/templates/logs.html`)

The "In Tokens" column header and cell value stay labelled "In Tokens". The cell value changes from `{{.InputTokens}}` to a computed sum. Since Go templates don't support arithmetic directly, add a template helper function `sumTokens` (registered alongside the existing `deref`, `add`, `subtract` helpers in the web package) that accepts the three int64 fields and returns their sum.

Cell render: `{{sumTokens .CacheCreationInputTokens .CacheReadInputTokens .InputTokens}}`

No column is added or removed — the table layout is unchanged.

### Step 13 — Log detail template (`internal/web/templates/log_detail.html`)

Replace the current single "Input Tokens" row in the token section with four rows:

```
Input Tokens:       {{.Log.InputTokens}}
Cache Write Tokens: {{.Log.CacheCreationInputTokens}}
Cache Read Tokens:  {{.Log.CacheReadInputTokens}}
Total In:           {{sumTokens .Log.InputTokens .Log.CacheCreationInputTokens .Log.CacheReadInputTokens}}
```

"Output Tokens" row is unchanged.

### Step 14 — Cost dashboard template (`internal/web/templates/costs.html`)

Locate the token summary section and add two stat cards / cells for "Cache Write Tokens" and "Cache Read Tokens" alongside the existing Input / Output token displays. Exact markup follows the existing Pico CSS pattern used for other stats.

### Step 15 — Template helper (`internal/web/`)

Register a `sumTokens` template function (variadic int64 → int64) in the same place existing template helpers (`add`, `subtract`, `deref`) are registered. This avoids duplicating arithmetic across multiple templates.

### Step 16 — Tests

Update existing unit tests that construct `PricingEntry`, `RequestLogEntry`, `StreamResult`, or call `Calculate` — they will fail to compile with the new fields. For `Calculate`, add table-driven test cases covering:
- zero cache tokens (regression)
- non-zero cache creation tokens only
- non-zero cache read tokens only
- both non-zero

For `extractTokens`, add a test case where the `message_start` payload includes cache token fields.

For `InsertRequestLog` / `GetRequestLog`, verify the round-trip of non-zero cache token values.

## Complexity Tracking

No constitution violations. Table left empty.
