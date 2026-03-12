# Implementation Plan: UI Log Improvements

**Branch**: `004-ui-log-improvements` | **Date**: 2026-03-11 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/004-ui-log-improvements/spec.md`

## Summary

Improve the llm-proxy admin UI across four areas: (1) auto-refreshing log list with polling failure indicator and new-entries banner, (2) structured conversation viewer on the detail page with system prompt disclosure, per-turn labels, tool-call rendering, and collapsible history, (3) per-category token cost breakdown on the detail page, and (4) UX polish (active nav, entry count, copy-to-clipboard, OS dark mode). No database schema changes are required. All work is confined to `internal/web/` and its embedded templates.

## Technical Context

**Language/Version**: Go 1.24+
**Primary Dependencies**: HTMX 2.0.4 (CDN), Pico CSS v2 (CDN), standard library `encoding/json`, `internal/pricing`, `internal/store`, `internal/provider/anthropic`
**Storage**: SQLite — no schema changes; one new store method (`CountLogsSince`)
**Testing**: `go test -race ./...` with `testify/require`; table-driven tests
**Target Platform**: Embedded HTTP server, served by the proxy binary
**Performance Goals**: Detail page render < 50ms; polling response < 20ms (single DB read)
**Constraints**: No new external dependencies; must compile with `CGO_ENABLED=0`; binary size must not grow materially
**Scale/Scope**: Single-user admin UI; no concurrency concerns on the UI read path

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Speed Above All | ✅ Pass | All changes are on the non-hot-path web UI. No proxy request lifecycle code is touched. The new `CountLogsSince` query is a simple aggregate on an indexed column. |
| II. Efficient Use of Resources | ✅ Pass | No new external dependencies. CDN assets already loaded. New Go types add negligible heap footprint. |
| III. Clean Abstractions | ✅ Pass | Parsing logic isolated in new `internal/web/conv.go`; cost breakdown in `internal/web/costview.go`. Handlers remain thin. Provider types used read-only for parsing. |
| IV. Correctness and Compatibility | ✅ Pass | No changes to proxy, translation, streaming, or provider logic. |
| V. Security by Default | ✅ Pass | All routes remain behind session middleware. `since_id` treated as untrusted input via parameterised query. `X-New-Count` exposes no user data. |

**Constitution check: ALL GATES PASS.**

## Project Structure

### Documentation (this feature)

```text
specs/004-ui-log-improvements/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/
│   └── ui-contracts.md  # Phase 1 output
└── tasks.md             # Phase 2 output (/speckit.tasks - not yet created)
```

### Source Code Changes

```text
internal/
└── web/
    ├── conv.go                          # NEW: parseConversation + ConversationData/ConvTurn/ConvBlock types
    ├── costview.go                      # NEW: computeCostBreakdown + CostBreakdown type
    ├── handlers.go                      # MODIFIED: Handlers struct, LogDetailPage, LogsAPIHandler
    ├── templates/
    │   ├── layout.html                  # MODIFIED: data-theme, active nav, shared JS
    │   ├── logs.html                    # MODIFIED: HTMX polling, status indicator, banner
    │   ├── log_detail.html              # MODIFIED: full redesign
    │   └── fragments/
    │       └── conv_viewer.html         # NEW: conversation viewer sub-template
    └── (all other files unchanged)

internal/store/
    ├── store.go                         # MODIFIED: add CountLogsSince to Store interface
    └── sqlite.go                        # MODIFIED: implement CountLogsSince
```

**Structure Decision**: Single-project layout. All changes are within `internal/web/` and `internal/store/`. No new packages needed; two new files (`conv.go`, `costview.go`) keep responsibilities singular per constitution Principle III.

## Phase 0: Research

All research is documented in [`research.md`](research.md). Key findings:

1. **HTMX polling**: `hx-trigger="every 10s"` on `<tbody>` with `hx-include="#filter-form"` preserves live filter values without resetting form fields.
2. **Failure indicator**: `hx-on:htmx:after-request` / `hx-on:htmx:response-error` inline event bindings update a `<span id="refresh-status">` element; failure sets CSS class `refresh-stale`.
3. **New-entries banner**: Existing poll includes a `since_id` param on page 2+; server sets `X-New-Count` response header; JS reads it via `evt.detail.xhr.getResponseHeader`.
4. **Dark mode**: `data-theme="auto"` on `<html>` activates Pico v2 OS-preference detection. No other changes required.
5. **Clipboard**: `navigator.clipboard.writeText()` guarded by `window.isSecureContext`; `execCommand('copy')` fallback for non-HTTPS.
6. **Conversation parsing**: Parse in Go handler; `System` field normalised to string; tool result IDs matched cross-turn; content truncated at 500 chars with `FullText` for expand.
7. **Cost breakdown**: Computed at render time from stored token counts + `pricing.Calculator`; no migration needed.

## Phase 1: Design

### New Go Types

Defined in `internal/web/conv.go` and `internal/web/costview.go`. See [`data-model.md`](data-model.md) for full field-by-field specification.

**Summary**:
- `ConversationData` — top-level parsed view (system prompt, latest prompt, response, history, parse failure flag, raw JSON fallbacks)
- `ConvTurn` — a single rendered turn (role + blocks)
- `ConvBlock` — typed content block (text, tool_use, tool_result, image/document label)
- `CostBreakdown` — per-category token counts and costs
- `LogDetailData` — top-level template data struct combining `*store.RequestLogDetail`, `*ConversationData`, `*CostBreakdown`

### Store Change

`CountLogsSince(ctx context.Context, sinceID string, params ListLogsParams) (int64, error)` added to `store.Store` interface and implemented in `sqlite.go`. Uses the existing filter parameter struct to match only entries the user can see. Parameterised query prevents injection. Returns `0` if `sinceID` is empty or not found.

### Handler Changes

**`Handlers` struct**: Add `calc *pricing.Calculator` field. Update `NewHandlers` signature.

**`LogDetailPage`**:
1. Fetch `*store.RequestLogDetail` (unchanged)
2. Call `parseConversation(log.RequestBody, log.ResponseBody)` → `*ConversationData`
3. Call `computeCostBreakdown(log, h.calc)` → `*CostBreakdown`
4. Build `LogDetailData{...}` and render `log_detail.html`

**`LogsAPIHandler`** (HTMX path only):
1. Parse `since_id` from query params
2. If non-empty and `Page > 1`: call `h.store.CountLogsSince(ctx, sinceID, params)` → set `X-New-Count` header
3. Existing fragment render unchanged

### Template Changes

#### `layout.html`
- `<html data-theme="auto">` — enables OS dark/light preference
- Active nav: pass `ActiveTab` to template (already in handler data); apply `aria-current="page"` and CSS class `.nav-active` to the matching nav link
- Add shared `<script>` block at bottom of `<body>` with: `copyText()`, `updateRefreshStatus()`, `markRefreshFailed()`, `showNewEntriesBanner()`
- CSS additions: `.nav-active`, `.refresh-status`, `.refresh-stale`, `.new-entries-banner`, `.conv-role`, `.conv-block`, `.conv-tool`, `.cost-headline`, `.token-detail`

#### `logs.html`
- Add `id="filter-form"` to the filter `<form>`
- Add new-entries banner `<div id="new-entries-banner" style="display:none">` above the table
- Add `<small id="refresh-status">` and Pause toggle `<button id="refresh-toggle">` in the toolbar
- Add HTMX polling attributes to `<tbody id="log-table-body">`:
  - `hx-get="/ui/api/logs"`, `hx-trigger="every 10s"`, `hx-include="#filter-form"`, `hx-swap="innerHTML"`
  - `hx-vals` with `page` and (conditionally) `since_id`
  - `hx-on:htmx:after-request`, `hx-on:htmx:response-error`
- Total count display: add "{{.Total}} entries" near pagination

#### `log_detail.html` (full redesign)

Layout structure:
```
[← Back to logs]

┌─────────────────────────────────────────────────────┐
│  $0.001234   |  1,234 in tokens   |  456 out tokens │  ← headline metrics
│  [Token Details ▼]                                  │  ← collapsible breakdown
│  ID: abc123 [⎘]  |  2026-03-11 15:04:05  |  200 ✓  │  ← secondary metadata
│  Model: claude-3-5-sonnet-20241022  |  128ms  |  SSE│
└─────────────────────────────────────────────────────┘

[System Prompt ▼]  (collapsed, if HasSystem)

Latest Prompt
┌─────────────────────────────────────────────────────┐
│  <user message text — truncated if long>            │
│  [Show more]                                        │
└─────────────────────────────────────────────────────┘

Response
┌─────────────────────────────────────────────────────┐
│  <assistant text or tool call block>                │
└─────────────────────────────────────────────────────┘

[Conversation History ▼]  (collapsed, N prior turns)

[View Raw JSON ▼]  (collapsed, shows request + response)
```

#### `fragments/conv_viewer.html` (new)

Sub-template `{{define "conv_viewer"}}` rendering a `ConvTurn` slice. Used by the history disclosure and optionally standalone turns.

### Contracts

Documented in [`contracts/ui-contracts.md`](contracts/ui-contracts.md). Summary:
- `GET /ui/api/logs`: new optional `since_id` param, new `X-New-Count` response header (HTMX only)
- `GET /ui/logs/:id`: same route, enriched template data
- No new HTTP routes

## Complexity Tracking

No constitution violations. No complexity justification needed.

## Artifacts Produced

| Artifact | Path | Status |
|----------|------|--------|
| Research | `specs/004-ui-log-improvements/research.md` | ✅ Complete |
| Data Model | `specs/004-ui-log-improvements/data-model.md` | ✅ Complete |
| UI Contracts | `specs/004-ui-log-improvements/contracts/ui-contracts.md` | ✅ Complete |
| Quickstart | `specs/004-ui-log-improvements/quickstart.md` | ✅ Complete |
| Tasks | `specs/004-ui-log-improvements/tasks.md` | ⏳ Pending (`/speckit.tasks`) |
