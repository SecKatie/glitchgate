# Tasks: UI Log Improvements

**Input**: Design documents from `/specs/004-ui-log-improvements/`
**Branch**: `004-ui-log-improvements`
**Prerequisites**: plan.md ✅, spec.md ✅, research.md ✅, data-model.md ✅, contracts/ ✅, quickstart.md ✅

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story. Tests are included per the spec's explicit coverage requirements in quickstart.md.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to
- All paths are relative to the repository root

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: New Go source files and shared template infrastructure that all user stories depend on.

- [x] T001 Create `internal/web/conv.go` with `ConversationData`, `ConvTurn`, `ConvBlock` type definitions (no logic yet)
- [x] T002 [P] Create `internal/web/costview.go` with `CostBreakdown` and `LogDetailData` type definitions (no logic yet)
- [x] T003 [P] Create `internal/web/templates/fragments/conv_viewer.html` as an empty `{{define "conv_viewer"}}` stub

**Checkpoint**: New files exist and `go build ./...` succeeds

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Infrastructure that all user stories depend on — store extension, handler struct update, and layout-level changes.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [x] T004 Add `CountLogsSince(ctx context.Context, sinceID string, params ListLogsParams) (int64, error)` to the `Store` interface in `internal/store/store.go`
- [x] T005 Implement `CountLogsSince` in `internal/store/sqlite.go` using a parameterised query (`timestamp > (SELECT timestamp FROM request_logs WHERE id = ?)`) with the same filter conditions as `ListRequestLogs`
- [x] T006 [P] Add `calc *pricing.Calculator` field to `Handlers` struct in `internal/web/handlers.go`; update `NewHandlers` constructor signature and all call sites in `cmd/serve.go`
- [x] T007 [P] Change `<html data-theme="light">` to `<html data-theme="auto">` in `internal/web/templates/layout.html` (enables OS dark/light mode — FR-014)
- [x] T008 [P] Add shared JavaScript helpers to the bottom of `<body>` in `internal/web/templates/layout.html`: `copyText()`, `updateRefreshStatus()`, `markRefreshFailed()`, `showNewEntriesBanner()`
- [x] T009 [P] Add CSS classes to `internal/web/templates/layout.html`: `.nav-active`, `.refresh-status`, `.refresh-stale`, `.new-entries-banner`, `.conv-role`, `.conv-block`, `.conv-tool`, `.cost-headline`, `.token-detail`
- [x] T010 Write table-driven tests for `CountLogsSince` in `internal/store/sqlite_test.go` (with and without filters, sinceID not found returns 0)

**Checkpoint**: `go test -race ./...` passes; `go build ./...` succeeds; `NewHandlers` compiles with the new `calc` parameter

---

## Phase 3: User Story 1 — Live Log Monitoring (Priority: P1) 🎯 MVP

**Goal**: The Logs list auto-refreshes every 10 seconds, preserves filter state, shows a status indicator, exposes a pause toggle, and shows a "new entries" banner on page 2+.

**Independent Test**: Open the Logs page, send a proxied request, verify a new row appears within 10 seconds without a page reload. Apply a filter, wait for a refresh, verify the filter is still set. Navigate to page 2, trigger new entries, verify the banner appears.

### Implementation for User Story 1

- [x] T011 [US1] Add `id="filter-form"` to the filter `<form>` element in `internal/web/templates/logs.html`
- [x] T012 [US1] Add HTMX polling attributes to `<tbody id="log-table-body">` in `internal/web/templates/logs.html`: `hx-get="/ui/api/logs"`, `hx-trigger="every 10s"`, `hx-include="#filter-form"`, `hx-swap="innerHTML"`, `hx-vals` with `page` and conditional `since_id`, `hx-on:htmx:after-request="updateRefreshStatus(event)"`, `hx-on:htmx:response-error="markRefreshFailed()"`
- [x] T013 [US1] Add `<div id="new-entries-banner" style="display:none" role="alert">` above the table in `internal/web/templates/logs.html` with count span, "view latest" link to `/ui/logs`, and dismiss button (FR-003a)
- [x] T014 [US1] Add `<small id="refresh-status">` status indicator and `<button id="refresh-toggle">` pause/resume control to the toolbar in `internal/web/templates/logs.html`; wire pause/resume via JavaScript that removes/restores the `hx-trigger` attribute (FR-002)
- [x] T015 [US1] Add total entry count display (`{{.Total}} entries`) near the pagination controls in `internal/web/templates/logs.html` (FR-012)
- [x] T016 [US1] Extend `LogsAPIHandler` in `internal/web/handlers.go` to: parse `since_id` query param; when non-empty and `Page > 1`, call `h.store.CountLogsSince(ctx, sinceID, params)` and set `X-New-Count` response header (per `contracts/ui-contracts.md`)

**Checkpoint**: Auto-refresh works end-to-end; status indicator updates; pause toggle stops/resumes polling; new-entries banner appears on page 2+; `go test -race ./...` passes

---

## Phase 4: User Story 5 — Token and Cost Breakdown (Priority: P3)

**Goal**: Log detail page shows cost and token counts as top-line figures with a collapsible per-category breakdown including cache token costs.

**Independent Test**: Open any log entry detail page — cost, input tokens, and output tokens are visible without scrolling. Click "Token Details" — cache write, cache read, uncached input, output tokens and their costs are all shown. For an unknown model, cost shows "Unknown".

**Note**: Implemented before US2 because it shares the `LogDetailData` struct and detail page redesign that US2 also needs; co-landing them avoids two full rewrites of `log_detail.html`.

### Implementation for User Story 5

- [x] T017 [US5] Implement `computeCostBreakdown(log *store.RequestLogDetail, calc *pricing.Calculator) *CostBreakdown` in `internal/web/costview.go` using the four-category pricing formula from `data-model.md`; set `PricingKnown=false` and nil cost pointers when model is not in pricing table (FR-015, FR-016, FR-017)
- [x] T018 [P] [US5] Write table-driven tests for `computeCostBreakdown` in `internal/web/cost_breakdown_test.go`: known pricing, unknown pricing, zero cache tokens
- [x] T019 [US5] Update `LogDetailPage` in `internal/web/handlers.go` to call `computeCostBreakdown` and populate `LogDetailData.Cost`; pass `LogDetailData` to the template
- [x] T020 [US5] Redesign the header section of `internal/web/templates/log_detail.html` to show cost, input tokens, and output tokens as top-line headline figures (`.cost-headline` CSS class); move secondary metadata (model, latency, streaming) below (FR-015)
- [x] T021 [US5] Add `<details><summary>Token Details</summary>…</details>` section to `internal/web/templates/log_detail.html` showing uncached input, cache write, cache read, output tokens and per-category costs; omit cost columns when `Cost.PricingKnown=false`; show "Unknown" for top-line cost when not known (FR-016, FR-017)

**Checkpoint**: Token/cost headline and breakdown render correctly for known and unknown models; `go test -race ./...` passes

---

## Phase 5: User Story 2 — Structured Conversation Viewer (Priority: P2)

**Goal**: The detail page parses request/response bodies and renders a structured, labelled conversation view with system prompt, latest prompt, response, and collapsible history.

**Independent Test**: Open a multi-turn log entry — latest user message and AI response are readable at the top without scrolling; prior turns are in a collapsed disclosure; tool calls show name and arguments; system prompt is in a collapsed section. Open a raw-body log entry — falls back to pretty-printed JSON with no error state.

### Implementation for User Story 2

- [x] T022 [US2] Implement `parseConversation(requestBody, responseBody string) *ConversationData` in `internal/web/conv.go` per `data-model.md` parsing rules: system prompt normalisation, message walk to split `History`/`LatestPrompt`, tool-result cross-turn ID matching, 500-char truncation, `ParseFailed` fallback
- [x] T023 [P] [US2] Write table-driven tests for `parseConversation` in `internal/web/conv_parser_test.go`: single-turn, multi-turn, tool-use with result, system prompt (string and array forms), parse failure, truncation
- [x] T024 [US2] Update `LogDetailPage` in `internal/web/handlers.go` to call `parseConversation` and populate `LogDetailData.Conversation`
- [x] T025 [US2] Implement `{{define "conv_viewer"}}` sub-template in `internal/web/templates/fragments/conv_viewer.html` to render a `[]ConvTurn` slice: role labels, text blocks (with `<details>` for truncated), tool-use blocks (name + input), tool-result blocks, image/document labels
- [x] T026 [US2] Update `internal/web/templates/log_detail.html` to add: collapsible `<details>` "System Prompt" section (when `HasSystem`); "Latest Prompt" section using `{{template "conv_viewer"}}`; "Response" section using `{{template "conv_viewer"}}`; collapsible "Conversation History" section with prior turns; collapsible "View Raw JSON" section showing `RawRequest` and `RawResponse` (always present — FR-009a)
- [x] T027 [US2] Add `<details><summary>Conversation History (N turns)</summary>…</details>` to `internal/web/templates/log_detail.html`; when `ParseFailed=true`, render only the Raw JSON section with a "Could not parse conversation" note (FR-009)

**Checkpoint**: Structured viewer renders for single-turn, multi-turn, and tool-call logs; parse failure falls back gracefully; `go test -race ./...` passes

---

## Phase 6: User Story 3 — Pretty-Printed Raw Bodies (Priority: P3)

**Goal**: Any raw JSON body shown on the detail page is indented and human-readable (not compact), and the display area is scrollable.

**Independent Test**: Open any log entry — raw bodies in the "View Raw JSON" section are indented JSON, not a single-line blob.

### Implementation for User Story 3

- [x] T028 [US3] In `parseConversation` (`internal/web/conv.go`), populate `RawRequest` and `RawResponse` using `json.MarshalIndent` on the parsed data (or `json.RawMessage` re-indent on the raw string); ensure `RawRequest`/`RawResponse` are always populated even on parse failure (FR-009, FR-010)
- [x] T029 [US3] Verify `internal/web/templates/log_detail.html` `<pre>` blocks for raw JSON have `overflow: auto` / `max-height` so they scroll rather than overflow (FR-010 — may already be partially covered by existing `.body-preview` CSS; adjust if needed)

**Checkpoint**: Raw JSON in "View Raw JSON" section is indented and scrollable for all log entries

---

## Phase 7: User Story 4 — UX Polish and Navigation (Priority: P4)

**Goal**: Active nav highlighting, copy-to-clipboard button on request ID, entry count on logs list (already done in T015 — verify only), and OS dark mode.

**Note**: Dark mode (T007) and entry count (T015) are already complete from earlier phases. This phase adds nav active state and copy button.

**Independent Test**: Navigate between Logs and Costs — the active page is highlighted. Click the copy button on a detail page ID — paste confirms the correct value. Set OS to dark mode — UI switches.

### Implementation for User Story 4

- [x] T030 [US4] Pass `ActiveTab string` in template data structs from `internal/web/handlers.go` for all page handlers (logs list, log detail, costs); add `ActiveTab` field to `LogDetailData` and equivalent data structs
- [x] T031 [US4] Apply active nav styling in `internal/web/templates/layout.html`: add `{{if eq .ActiveTab "logs"}}aria-current="page" class="nav-active"{{end}}` (and equivalent for "costs") to the nav links
- [x] T032 [US4] Add copy-to-clipboard button next to the request ID in `internal/web/templates/log_detail.html`: `<button class="outline secondary copy-btn" onclick="copyText(this, '{{.Log.ID}}')" title="Copy ID">⎘</button>` — uses `copyText()` already added in T008 (FR-013)

**Checkpoint**: Active nav highlights correctly on all pages; copy button copies ID; dark mode activates with OS preference; `go build ./...` and `go test -race ./...` pass

---

## Phase 8: Polish & Validation

**Purpose**: Final checks, linting, and quickstart validation

- [x] T033 Run `make lint` and fix any `golangci-lint` findings in new/modified files
- [x] T034 [P] Run `make audit` (`gosec` + `govulncheck`) and address any findings
- [x] T035 Run `make test` (`go test -race ./...`) and confirm all tests pass
- [ ] T036 [P] Manually verify all quickstart.md test scenarios (auto-refresh, new-entries banner, conversation viewer, cost breakdown, UX polish) against a running instance

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (Setup)**: No dependencies — start immediately
- **Phase 2 (Foundational)**: Depends on Phase 1 — **blocks all user story phases**
- **Phase 3 (US1 auto-refresh)**: Depends on Phase 2
- **Phase 4 (US5 cost breakdown)**: Depends on Phase 2
- **Phase 5 (US2 conversation viewer)**: Depends on Phase 2; shares detail page with Phase 4 — best started after T019-T021 land to avoid merge conflicts on `log_detail.html`
- **Phase 6 (US3 pretty-print)**: Depends on Phase 5 (reuses `parseConversation`)
- **Phase 7 (US4 polish)**: Depends on Phase 2; T030-T031 can run in parallel with Phase 3; T032 depends on Phase 4/5 (detail page must exist)
- **Phase 8 (Polish)**: Depends on all prior phases

### User Story Dependencies

| Story | Depends On | Can Parallelise With |
|-------|-----------|----------------------|
| US1 (auto-refresh) | Phase 2 | US5, US4 nav tasks |
| US5 (cost breakdown) | Phase 2 | US1, US4 nav tasks |
| US2 (conversation viewer) | Phase 2 + US5 detail page | US1 |
| US3 (pretty-print) | US2 (`parseConversation`) | US1, US5 |
| US4 (UX polish) | Phase 2 (nav); US5+US2 (copy button) | US1 |

### Parallel Opportunities Within Stories

- **Phase 2**: T005, T006, T007, T008, T009 all touch different files — run in parallel after T004
- **Phase 4**: T017 (logic) and T018 (tests) can be written in parallel
- **Phase 5**: T022 (logic) and T023 (tests) can be written in parallel
- **Phase 8**: T033, T034, T036 can run in parallel

---

## Parallel Execution Examples

```
# Phase 2 — after T004 (store interface) is done:
T005  implement CountLogsSince in sqlite.go
T006  add calc to Handlers struct
T007  data-theme="auto" in layout.html
T008  shared JS helpers in layout.html
T009  CSS classes in layout.html

# Phase 3 + Phase 4 — start together after Phase 2:
[terminal A]  T011-T016  (auto-refresh — logs.html + handler)
[terminal B]  T017-T021  (cost breakdown — costview.go + log_detail.html)
```

---

## Implementation Strategy

### MVP (User Story 1 Only)

1. Complete Phase 1 (Setup) + Phase 2 (Foundational)
2. Complete Phase 3 (US1 — auto-refresh)
3. **Validate**: new rows appear within 10s; filter preserved; banner on page 2+
4. Optionally ship — the proxy is already more useful with live monitoring

### Recommended Incremental Order

1. Phase 1 + 2 → Foundation ready
2. Phase 3 (US1) + Phase 4 (US5) in parallel → Auto-refresh + cost headline
3. Phase 5 (US2) → Structured conversation viewer
4. Phase 6 (US3) → Pretty-print fallback (small, follows US2)
5. Phase 7 (US4) → Nav + copy button polish
6. Phase 8 → Final lint / audit / validation

---

## Task Count Summary

| Phase | Story | Tasks | Notes |
|-------|-------|-------|-------|
| Phase 1 | Setup | 3 | File stubs |
| Phase 2 | Foundational | 7 | Blocks all stories |
| Phase 3 | US1 auto-refresh (P1) | 6 | MVP |
| Phase 4 | US5 cost breakdown (P3) | 5 | Parallel with US1 |
| Phase 5 | US2 conversation viewer (P2) | 6 | Main feature work |
| Phase 6 | US3 pretty-print (P3) | 2 | Depends on US2 |
| Phase 7 | US4 UX polish (P4) | 3 | Mostly parallel |
| Phase 8 | Polish | 4 | Final validation |
| **Total** | | **36** | |

**Tests included**: T010 (CountLogsSince), T018 (cost breakdown), T023 (conversation parser) — covering the three new Go functions with table-driven tests per quickstart.md requirements.
