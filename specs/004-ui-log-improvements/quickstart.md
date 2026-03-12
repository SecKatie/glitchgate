# Quickstart: UI Log Improvements

**Feature**: 004-ui-log-improvements
**Branch**: `004-ui-log-improvements`

---

## Prerequisites

Standard project toolchain (Go 1.24+, `make`, `golangci-lint`, `gofumpt`, `goimports`). No new tools required.

---

## Build & Run

```bash
# Build binary
make build

# Start the proxy (adjust config as needed)
./llm-proxy serve

# Open the UI
open http://localhost:8080/ui/logs
```

---

## Testing This Feature

### Auto-Refresh (User Story 1)

1. Open `http://localhost:8080/ui/logs` — the table footer should show "Updated just now."
2. Send any proxied request through the proxy from another terminal.
3. Within 10 seconds, the new log row should appear without a page reload.
4. Click the **Pause** toggle — verify the table stops refreshing.
5. Simulate a failure: stop the proxy process while the page is open. Within ~10s the status indicator should turn amber with "Refresh failed." Restart the proxy — it should recover automatically.

### New-Entries Banner (Page 2+)

1. Ensure you have more than 50 log entries.
2. Navigate to page 2 (`/ui/logs?page=2`).
3. Send new requests through the proxy.
4. Within 10 seconds a banner should appear: "N new entries — view latest."
5. Click the banner link — should navigate to page 1.
6. Click the **×** on the banner — should dismiss without navigating.

### Structured Conversation Viewer (User Story 2)

1. Open any log entry that used the Anthropic API.
2. Verify the page shows a **Latest Prompt** section and a **Response** section at the top.
3. For a multi-turn request: verify prior turns are hidden in a collapsed **Conversation History** disclosure. Expand it and verify each turn is labelled (User / Assistant).
4. For a request with tool calls: verify the **Response** section shows the tool name and arguments in a labelled layout.
5. For a request with a system prompt: verify a collapsed **System Prompt** section appears above the conversation.
6. Click **View Raw JSON** — verify the full raw request and response bodies appear pretty-printed.

### Token and Cost Breakdown (User Story 5)

1. Open any log entry detail page.
2. Verify **Cost**, **Total Input Tokens**, and **Output Tokens** are visible at the top without scrolling.
3. Click **Token Details** — verify the expanded section shows uncached input, cache write, cache read, and output token counts with per-category cost values.
4. For a request with an unrecognised model: verify the top-line cost shows "Unknown" and per-category costs are absent in the breakdown.

### UX Polish (User Story 4)

1. Navigate to Logs — verify "Logs" in the nav is visually highlighted.
2. Navigate to Costs — verify "Costs" is highlighted, "Logs" is not.
3. On the log list, verify the total entry count is visible (e.g., "247 entries").
4. On a detail page, click the copy button next to the Request ID — paste into a text editor to verify the correct ID was copied.
5. Set your OS to dark mode — reload the UI and verify the theme adapts.

---

## Running Tests

```bash
# All tests with race detector
make test

# Linter
make lint

# Security audit
make audit
```

New test coverage to verify:
- `internal/web/conv_parser_test.go` — table-driven tests for `parseConversation` covering single-turn, multi-turn, tool-use, system prompt, parse failure, truncation.
- `internal/web/cost_breakdown_test.go` — tests for `computeCostBreakdown` covering known pricing, unknown pricing, zero cache tokens.
- `internal/store/sqlite_test.go` (extended) — test for `CountLogsSince` with and without filters.

---

## Key Files Changed

| File | Change |
|------|--------|
| `internal/web/handlers.go` | Add `calc *pricing.Calculator` to `Handlers`; update `LogDetailPage` to build `LogDetailData`; extend `LogsAPIHandler` for `since_id` + `X-New-Count` |
| `internal/web/conv.go` (new) | `parseConversation`, `ConversationData`, `ConvTurn`, `ConvBlock` types and parsing logic |
| `internal/web/costview.go` (new) | `computeCostBreakdown`, `CostBreakdown` type |
| `internal/store/store.go` | Add `CountLogsSince` to `Store` interface |
| `internal/store/sqlite.go` | Implement `CountLogsSince` |
| `internal/web/templates/layout.html` | `data-theme="auto"`; active nav highlighting; shared JS helpers (`copyText`, `updateRefreshStatus`, etc.) |
| `internal/web/templates/logs.html` | HTMX polling attrs on `<tbody>`; status indicator; new-entries banner |
| `internal/web/templates/log_detail.html` | Full redesign: headline metrics, Token Details, System Prompt, Conversation Viewer, Raw JSON toggle |
| `internal/web/templates/fragments/conv_viewer.html` (new) | Conversation viewer fragment |
