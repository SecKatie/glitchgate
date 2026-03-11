# Feature Specification: Cache Token Usage Logging

**Feature Branch**: `003-cache-token-logging`
**Created**: 2026-03-11
**Status**: Draft
**Input**: User description: "I want to improve the request and response logging to better capture usage. Currently we do not capture cache tokens."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Accurate Cache Token Counts in Logs (Priority: P1)

An operator reviewing request logs wants to see the full token breakdown for a request — including how many tokens were served from cache versus written to cache versus billed as regular input tokens. Currently those fields are missing and the log shows only `input_tokens` and `output_tokens`, making it impossible to audit real usage.

**Why this priority**: Cache hits can represent the majority of tokens consumed on long-context workloads. Without capturing them, logged token counts and cost estimates are significantly understated and untrustworthy.

**Independent Test**: Can be fully tested by sending a single request that triggers prompt caching, then retrieving the stored log record and verifying all cache token fields are populated correctly.

**Acceptance Scenarios**:

1. **Given** the proxy receives a response containing `cache_creation_input_tokens`, **When** the log record is stored, **Then** the log record contains `cache_creation_input_tokens` equal to the value in the upstream response.
2. **Given** the proxy receives a response containing `cache_read_input_tokens`, **When** the log record is stored, **Then** the log record contains `cache_read_input_tokens` equal to the value in the upstream response.
3. **Given** a response has no cache fields (cache not used), **When** the log record is stored, **Then** cache token fields default to zero, preserving backward compatibility.
4. **Given** a streaming response, **When** cache usage appears in the `message_start` SSE event, **Then** all cache token fields are extracted and stored identically to non-streaming responses.

---

### User Story 2 - Accurate Cost Estimates Including Cache Pricing (Priority: P2)

An operator using the cost dashboard wants the estimated cost per request to reflect the actual billing breakdown, since cache reads and cache writes carry different per-token prices than regular input tokens.

**Why this priority**: Incorrect cost estimates erode trust in the dashboard and may cause budget overruns if operators rely on the proxy for spend tracking.

**Independent Test**: Can be fully tested by verifying the cost calculation for a known request with known cache token counts matches the expected billing amount.

**Acceptance Scenarios**:

1. **Given** a request with `cache_creation_input_tokens` and a known per-token write price, **When** cost is calculated, **Then** the estimated cost includes a cache write component priced at the cache-write rate (distinct from the standard input rate).
2. **Given** a request with `cache_read_input_tokens` and a known per-token read price, **When** cost is calculated, **Then** the estimated cost includes a cache read component priced at the cache-read rate (significantly lower than standard input).
3. **Given** a request with zero cache tokens, **When** cost is calculated, **Then** the result is identical to the current calculation (no regression).

---

### User Story 3 - Rolled-Up Token Counts on the Logs List Page (Priority: P3)

An operator scanning the request log table wants the "In Tokens" column to represent the total input tokens for the request — regular input plus any cache-written and cache-read tokens — so the number reflects the full context size at a glance without requiring the detail view.

**Why this priority**: Showing only `input_tokens` in the list after this feature lands would be misleading — on cached requests that number could be 1 while the real context is 57,000+ tokens.

**Independent Test**: Can be fully tested by inspecting the logs list HTML for a known cached request and confirming the "In Tokens" cell shows the sum rather than just the bare input token count.

**Acceptance Scenarios**:

1. **Given** a request log with `input_tokens=1`, `cache_creation_input_tokens=173`, `cache_read_input_tokens=57686`, **When** the logs list page renders that row, **Then** the "In Tokens" cell displays `57860` (the sum of all three).
2. **Given** a request log with zero cache tokens, **When** the logs list page renders that row, **Then** the "In Tokens" cell displays `input_tokens` unchanged (no regression).

---

### User Story 4 - Full Token Breakdown on the Log Detail Page (Priority: P4)

An operator clicking into a specific log entry wants to see the complete breakdown of input tokens — regular, cache write, cache read, and total — so they can understand exactly what was billed and why.

**Why this priority**: The list page intentionally shows only the total; the detail page is the appropriate place for the full picture.

**Independent Test**: Can be fully tested by loading the detail page for a known cached request and verifying all four token values (Input, Cache Write, Cache Read, Total In) are displayed and that Total In equals their sum.

**Acceptance Scenarios**:

1. **Given** a log with non-zero cache tokens, **When** the detail page is viewed, **Then** it displays separate rows for Input Tokens, Cache Write Tokens, Cache Read Tokens, and Total In (the computed sum of all three).
2. **Given** a log with zero cache tokens, **When** the detail page is viewed, **Then** Cache Write Tokens and Cache Read Tokens display `0` and Total In equals Input Tokens.

---

### User Story 5 - Cache Token Visibility in the Cost Dashboard (Priority: P5)

An operator browsing the cost dashboard wants to see aggregated cache token counts alongside regular token counts so they can understand the cache efficiency of their workloads and verify that prompt caching is actually saving money.

**Why this priority**: Visibility into cache efficiency is the business justification for enabling prompt caching; without it operators cannot evaluate the investment.

**Independent Test**: Can be fully tested by loading the cost summary API endpoint and verifying that cache token totals are present in the response, even before any UI changes are made.

**Acceptance Scenarios**:

1. **Given** historical request logs that include cache token data, **When** the cost summary API is queried, **Then** the response includes `total_cache_creation_tokens` and `total_cache_read_tokens` aggregated across all matching logs.
2. **Given** the cost dashboard page, **When** an operator views the summary, **Then** cache write tokens and cache read tokens appear alongside regular input and output token totals.
3. **Given** a date filter applied on the dashboard, **When** the operator filters by date range, **Then** cache token aggregates respect the same filter as all other metrics.

---

### Edge Cases

- What happens when the upstream response includes a `cache_creation` sub-object (ephemeral 5m vs 1h breakdown) in addition to the top-level `cache_creation_input_tokens`? The top-level field is authoritative for logging; the ephemeral breakdown is ignored in this feature.
- What happens when the upstream provider does not return cache fields at all (e.g., cache not used or unsupported model)? All cache token fields default to zero; cost calculation does not error.
- What happens when a streaming response's `message_start` event contains cache fields but subsequent events do not? Each event is parsed independently; missing fields in one event do not overwrite valid values from another.
- What happens to existing log records in the database that predate this feature? They have zero-value cache token columns and are treated as having no cache usage; no historical backfill is required.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST capture `cache_creation_input_tokens` from upstream API responses and store the value in the request log record.
- **FR-002**: The system MUST capture `cache_read_input_tokens` from upstream API responses and store the value in the request log record.
- **FR-003**: The system MUST extract cache token fields from streaming SSE responses using the `message_start` event payload, consistent with how existing input tokens are extracted.
- **FR-004**: The database schema MUST be extended with non-nullable integer columns for `cache_creation_input_tokens` and `cache_read_input_tokens`, both defaulting to `0`, applied via a schema migration that preserves all existing data.
- **FR-005**: The cost calculator MUST incorporate `cache_creation_input_tokens` and `cache_read_input_tokens` into the estimated cost using their respective per-token rates for each model where those rates are configured.
- **FR-006**: For models that do not have cache pricing configured, the system MUST treat cache tokens as zero additional cost and MUST emit a log warning indicating that cache pricing is unavailable for that model.
- **FR-007**: The cost summary API response MUST include `total_cache_creation_tokens` and `total_cache_read_tokens` totals aggregated across all matching log records.
- **FR-008**: The per-model and per-key cost breakdown responses MUST include `cache_creation_tokens` and `cache_read_tokens` fields alongside the existing `input_tokens` and `output_tokens` fields.
- **FR-009**: The web dashboard cost summary view MUST display cache write token totals and cache read token totals alongside regular input and output token totals.
- **FR-010**: All changes to data structures MUST be backward-compatible: code paths that do not supply cache token values receive zero-value defaults rather than errors.
- **FR-011**: The request log list page MUST display a single "In Tokens" value per row equal to the sum of `input_tokens + cache_creation_input_tokens + cache_read_input_tokens`, computed at render time from stored fields.
- **FR-012**: The request log detail page MUST display Input Tokens, Cache Write Tokens, and Cache Read Tokens as separate labelled values, plus a "Total In" value equal to their sum.

### Key Entities

- **Request Log**: A stored record of one proxied API request. Currently tracks `input_tokens` and `output_tokens`; gains `cache_creation_input_tokens` and `cache_read_input_tokens`.
- **Usage Payload**: The `usage` object in upstream API responses. Contains `input_tokens`, `output_tokens`, `cache_creation_input_tokens`, and `cache_read_input_tokens`.
- **Cache Pricing Entry**: Per-model pricing configuration that adds a cache write rate and a cache read rate alongside existing input and output rates.
- **Cost Summary**: Aggregated cost and token totals returned by the dashboard API. Gains `total_cache_creation_tokens` and `total_cache_read_tokens`.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: After deployment, 100% of log records for requests that receive a cache hit or cache write contain non-zero values in the cache token columns, matching the upstream response values exactly.
- **SC-002**: Cost estimates for requests with prompt caching differ from pre-feature estimates by a predictable, auditable amount equal to `cache_creation_tokens × cache_write_rate + cache_read_tokens × cache_read_rate` for all models with configured cache pricing.
- **SC-003**: The cost dashboard displays cache token totals with no visible additional latency compared to the current dashboard load time.
- **SC-004**: All existing tests pass without modification, confirming no regression in behavior for requests that do not use caching.
- **SC-005**: The schema migration completes successfully on a database containing existing request log records, and all pre-existing records remain readable with zero-value cache token fields.

## Assumptions

- The top-level `cache_creation_input_tokens` and `cache_read_input_tokens` fields in the upstream `usage` object are the authoritative counts for billing and logging. The nested `cache_creation` sub-object (ephemeral 5m/1h breakdown) is out of scope.
- Cache pricing follows Anthropic's published rates: cache write tokens are billed at 25% above the standard input rate, and cache read tokens are billed at 10% of the standard input rate. These rates will be added to the existing per-model pricing configuration.
- The `service_tier` and `inference_geo` fields present in the usage payload are out of scope for this feature.
- No historical backfill of cache token data for pre-existing log records is required.
- Web UI changes are limited to surfacing the new totals in the existing cost dashboard; a dedicated cache efficiency analytics page is out of scope.
