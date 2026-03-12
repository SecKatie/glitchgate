# Tasks: Cache Token Usage Logging

**Input**: Design documents from `/specs/003-cache-token-logging/`
**Prerequisites**: plan.md ✅, spec.md ✅, research.md ✅, data-model.md ✅

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1–US5)

---

## Phase 1: Foundational (Blocking Prerequisites)

**Purpose**: Type and schema changes that every user story depends on. No user story work can begin until this phase is complete.

- [X] T001 Create DB migration `internal/store/migrations/003_add_cache_tokens.sql` — two `ALTER TABLE` statements adding `cache_creation_input_tokens` and `cache_read_input_tokens` INTEGER NOT NULL DEFAULT 0 to `request_logs`
- [X] T002 [P] Extend `Usage` struct in `internal/provider/anthropic/types.go` — add `CacheCreationInputTokens int64 \`json:"cache_creation_input_tokens"\`` and `CacheReadInputTokens int64 \`json:"cache_read_input_tokens"\``
- [X] T003 [P] Extend `Response` struct in `internal/provider/provider.go` — add `CacheCreationInputTokens int64` and `CacheReadInputTokens int64` alongside existing `InputTokens`/`OutputTokens`
- [X] T004 [P] Extend store types in `internal/store/store.go` — add `CacheCreationInputTokens int64` and `CacheReadInputTokens int64` to `RequestLogEntry` and `RequestLogSummary`; add `TotalCacheCreationTokens int64` and `TotalCacheReadTokens int64` to `CostSummary`; add `CacheCreationTokens int64` and `CacheReadTokens int64` to `CostBreakdownEntry`

**Checkpoint**: All shared types updated — user story implementation can now begin

---

## Phase 2: User Story 1 — Accurate Cache Token Counts in Logs (Priority: P1) 🎯 MVP

**Goal**: Capture `cache_creation_input_tokens` and `cache_read_input_tokens` from upstream API responses (streaming and non-streaming) and persist them in the database.

**Independent Test**: Send a request that triggers prompt caching, retrieve the stored log record via `GET /ui/api/logs`, and verify the two cache token fields contain non-zero values matching the upstream response.

- [X] T005 [P] [US1] Extract cache tokens from non-streaming response in `internal/provider/anthropic/client.go` — after populating `provResp.InputTokens` and `provResp.OutputTokens`, also set `provResp.CacheCreationInputTokens = msgResp.Usage.CacheCreationInputTokens` and `provResp.CacheReadInputTokens = msgResp.Usage.CacheReadInputTokens`
- [X] T006 [P] [US1] Extend `StreamResult` and update `extractTokens` in `internal/proxy/stream.go` — add `CacheCreationInputTokens int64` and `CacheReadInputTokens int64` to `StreamResult`; in `extractTokens` add pointer params for the two new fields and populate them from `envelope.Message.Usage` in the `message_start` branch; update `RelaySSEStream` to initialise and return the new fields
- [X] T007 [US1] Thread cache tokens through `logRequest` in `internal/proxy/handler.go` — add `cacheCreationTokens, cacheReadTokens int64` to the `logRequest` signature; in `handleNonStreaming` pass `resp.CacheCreationInputTokens, resp.CacheReadInputTokens`; in `handleStreaming` pass `result.CacheCreationInputTokens, result.CacheReadInputTokens`; in the error path pass `0, 0`; inside `logRequest` populate `entry.CacheCreationInputTokens` and `entry.CacheReadInputTokens`
- [X] T008 [US1] Update `InsertRequestLog`, `ListRequestLogs`, and `GetRequestLog` in `internal/store/sqlite.go` — add `cache_creation_input_tokens` and `cache_read_input_tokens` to the INSERT column list and args; add both columns to the SELECT and Scan calls in `ListRequestLogs` and `GetRequestLog`
- [X] T009 [P] [US1] Mirror SQL changes in `queries/request_logs.sql` — update `InsertRequestLog`, `ListRequestLogs`, and `GetRequestLog` queries to match `sqlite.go`

**Checkpoint**: Cache token counts are captured end-to-end and stored. US1 is independently testable.

---

## Phase 3: User Story 2 — Accurate Cost Estimates Including Cache Pricing (Priority: P2)

**Goal**: Extend the cost calculator to price `cache_creation_input_tokens` and `cache_read_input_tokens` at their respective rates and include the cache cost component in `estimated_cost_usd`.

**Independent Test**: For a known request with `cache_creation_input_tokens=173` and `cache_read_input_tokens=57686` on `claude-sonnet-4-20250514`, verify the estimated cost equals `(1×3.00 + 173×3.75 + 57686×0.30 + 1×15.00) / 1_000_000`.

- [X] T010 [P] [US2] Update `PricingEntry` and `Calculate` in `internal/pricing/calculator.go` — add `CacheWritePerMillion float64` and `CacheReadPerMillion float64` to `PricingEntry`; update `Calculate` signature to accept `cacheCreationTokens, cacheReadTokens int64`; add `float64(cacheCreationTokens)*entry.CacheWritePerMillion + float64(cacheReadTokens)*entry.CacheReadPerMillion` to the cost sum; if `CacheWritePerMillion == 0` and either cache token count is non-zero, emit `log.Printf("WARNING: cache pricing not configured for model %s", upstreamModel)`
- [X] T011 [P] [US2] Add cache rates to `DefaultPricing` in `internal/pricing/defaults.go` — set `CacheWritePerMillion` to `InputPerMillion * 1.25` and `CacheReadPerMillion` to `InputPerMillion * 0.10` for all three default models (sonnet: 3.75/0.30, opus: 18.75/1.50, haiku: 1.00/0.08)
- [X] T012 [US2] Update `calculator.Calculate` call sites in `internal/proxy/handler.go` — pass `cacheCreationTokens` and `cacheReadTokens` to both `h.calculator.Calculate(...)` calls in `handleNonStreaming` and `handleStreaming` (these are now available from T007)

**Checkpoint**: Estimated cost correctly incorporates cache pricing. US2 independently testable via cost calculation unit tests.

---

## Phase 4: User Story 3 — Rolled-Up Token Counts on Logs List Page (Priority: P3)

**Goal**: The "In Tokens" column on the request log list displays the sum of all input token types, not just bare `input_tokens`.

**Independent Test**: Load `/ui/logs` for a known cached request; confirm the "In Tokens" cell shows `input_tokens + cache_creation_input_tokens + cache_read_input_tokens`.

- [X] T013 [US3] Add `sumTokens` variadic template helper to `templateFuncs()` in `internal/web/handlers.go` — register `"sumTokens": func(vals ...int64) int64 { var s int64; for _, v := range vals { s += v }; return s }` alongside the existing `add`, `subtract`, `addInt64` helpers
- [X] T014 [US3] Update "In Tokens" cell in `internal/web/templates/logs.html` — replace the existing `{{.InputTokens}}` (or equivalent) cell value with `{{sumTokens .InputTokens .CacheCreationInputTokens .CacheReadInputTokens}}`; column header stays "In Tokens"

**Checkpoint**: List page shows rolled-up input token total. No new columns added.

---

## Phase 5: User Story 4 — Full Token Breakdown on Log Detail Page (Priority: P4)

**Goal**: The log detail page shows Input Tokens, Cache Write Tokens, Cache Read Tokens, and Total In as separate labelled rows, followed by Output Tokens.

**Independent Test**: Load `/ui/logs/{id}` for a known cached request; confirm four distinct token rows appear and Total In equals the sum of the other three.

- [X] T015 [US4] Update token section in `internal/web/templates/log_detail.html` — replace the single "Input Tokens" row with four rows: `Input Tokens: {{.Log.InputTokens}}`, `Cache Write Tokens: {{.Log.CacheCreationInputTokens}}`, `Cache Read Tokens: {{.Log.CacheReadInputTokens}}`, `Total In: {{sumTokens .Log.InputTokens .Log.CacheCreationInputTokens .Log.CacheReadInputTokens}}`; "Output Tokens" row unchanged

**Checkpoint**: Detail page shows full input token breakdown with computed total.

---

## Phase 6: User Story 5 — Cache Tokens in Cost Dashboard (Priority: P5)

**Goal**: The cost summary API and dashboard surface aggregated `total_cache_creation_tokens` and `total_cache_read_tokens`.

**Independent Test**: Query `GET /ui/api/costs`; confirm `total_cache_creation_tokens` and `total_cache_read_tokens` appear in the JSON response and match the sum of the corresponding columns in the database.

- [X] T016 [US5] Update `GetCostSummary` and `GetCostBreakdown` in `internal/store/sqlite.go` — add `COALESCE(SUM(cache_creation_input_tokens), 0)` and `COALESCE(SUM(cache_read_input_tokens), 0)` to the `GetCostSummary` SELECT and Scan into `cs.TotalCacheCreationTokens` / `cs.TotalCacheReadTokens`; add the same aggregations to both `key` and `model` branches of `GetCostBreakdown` and Scan into `e.CacheCreationTokens` / `e.CacheReadTokens`
- [X] T017 [P] [US5] Mirror SQL changes in `queries/costs.sql` — update `GetTotalCost`, `GetCostByModel`, and `GetCostByKey` to include the two cache token aggregation columns
- [X] T018 [US5] Extend API response types and mapping in `internal/web/cost_handlers.go` — add `TotalCacheCreationTokens int64 \`json:"total_cache_creation_tokens"\`` and `TotalCacheReadTokens int64 \`json:"total_cache_read_tokens"\`` to `costSummaryResponse`; add `CacheCreationTokens int64 \`json:"cache_creation_tokens"\`` and `CacheReadTokens int64 \`json:"cache_read_tokens"\`` to `costBreakdownEntryJSON`; populate both in `CostSummaryHandler` from the store results
- [X] T019 [US5] Add cache token stat displays to `internal/web/templates/costs.html` — add "Cache Write Tokens" and "Cache Read Tokens" stat cards to the token summary section, following the existing Pico CSS stat card pattern used for Input / Output tokens

**Checkpoint**: Cost API and dashboard expose cache token aggregates. US5 independently testable.

---

## Phase 7: Polish & Tests

**Purpose**: Add test coverage for changed logic; verify no regressions.

- [X] T020 [P] Create `internal/pricing/calculator_test.go` with table-driven tests for `Calculate` covering: zero cache tokens (regression parity with current behaviour), non-zero `cacheCreationTokens` only, non-zero `cacheReadTokens` only, both non-zero, and model-not-found returning nil
- [X] T021 [P] Create `internal/proxy/stream_test.go` with a test for `extractTokens` — add a case where the `message_start` payload contains `cache_creation_input_tokens` and `cache_read_input_tokens` and assert they are captured correctly alongside `input_tokens`
- [X] T022 [P] Add cache token round-trip test to `internal/store/cost_queries_test.go` — insert a `RequestLogEntry` with non-zero cache token values, retrieve it with `GetRequestLog`, and assert both fields are preserved exactly

---

## Dependencies & Execution Order

### Phase Dependencies

- **Foundational (Phase 1)**: No dependencies — start immediately; T002, T003, T004 can run in parallel after T001
- **US1 (Phase 2)**: Depends on Phase 1 — T005 and T006 parallel, T007 depends on both, T008 depends on T004, T009 parallel with T008
- **US2 (Phase 3)**: Depends on Phase 1 — T010 and T011 parallel, T012 depends on T007 (from US1) and T010
- **US3 (Phase 4)**: Depends on Phase 1 and T008 (so store returns cache fields to template) — T013 then T014
- **US4 (Phase 5)**: Depends on T013 (for `sumTokens`) and T008 — T015
- **US5 (Phase 6)**: Depends on T004 — T016, T017 parallel; T018 depends on T016; T019 depends on T018
- **Polish (Phase 7)**: Depends on T010 (US2), T006 (US1), T008 (US1)

### User Story Dependencies

| Story | Hard dependency | Can parallelise with |
|---|---|---|
| US1 (P1) | Phase 1 | — |
| US2 (P2) | Phase 1 + T007 from US1 | US3, US5 (different files) |
| US3 (P3) | Phase 1 + T008 from US1 | US2, US5 |
| US4 (P4) | T013 from US3 | US2, US5 |
| US5 (P5) | Phase 1 | US1, US2, US3 |

### Parallel Opportunities Within US1

```
After Phase 1 is complete:
  Parallel: T005 (client.go) + T006 (stream.go)
  Then:     T007 (handler.go) — needs T005 + T006
  Parallel: T008 (sqlite.go) + T009 (queries/request_logs.sql)
```

---

## Implementation Strategy

### MVP (US1 only — 9 tasks)

1. Complete Phase 1 (T001–T004)
2. Complete Phase 2/US1 (T005–T009)
3. **Validate**: Cache tokens appear in log records; list/detail queries return them
4. Ship — cost estimates and dashboard cache totals can follow

### Incremental Delivery

1. Phase 1 → foundation ready
2. US1 → cache tokens captured ✅
3. US2 → cost estimates accurate ✅
4. US3 + US4 → UI shows correct token counts ✅
5. US5 → dashboard shows cache aggregates ✅
6. Phase 7 → test coverage locked in ✅

---

## Notes

- T002, T003, T004 can all run in parallel after T001 — they touch different files
- T005 and T006 can run in parallel — `client.go` and `stream.go` are independent
- T010 and T011 can run in parallel — `calculator.go` and `defaults.go` are independent
- T016 and T017 can run in parallel — `sqlite.go` and `queries/costs.sql` are independent
- All Phase 7 test tasks can run in parallel — separate test files
- Commit after each checkpoint to keep changes reviewable
