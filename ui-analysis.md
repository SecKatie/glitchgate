# UI Usability Analysis — llm-proxy

**Date**: 2026-03-11
**Method**: Manual exploration via headless Chrome (rodney) against localhost:4000
**Data**: 710 log entries, 692 requests, 4 models, single day of usage

---

## Critical Issues

### 1. Root path returns bare 404

**Location**: Route configuration (`cmd/serve.go`)

Navigating to `localhost:4000/` shows unstyled text `404 page not found` with no link or redirect. Anyone who types just the hostname gets stranded with no recovery path.

**Fix**: Redirect `/` to `/ui/logs` (or `/ui/login` if unauthenticated).

---

### 2. "Total Input Tokens" headline is misleading

**Location**: `internal/web/templates/log_detail.html` lines 18–21

The detail page headline shows a single number that sums uncached + cache write + cache read tokens. A request with 107,193 cache-read tokens and 1 uncached token displays as "107,504 TOTAL INPUT TOKENS" next to "$0.034" — the relationship between that token count and that cost is baffling until you expand Token Details.

Cache reads cost $0.30/MT vs. $3.00/MT for uncached input (10x cheaper). Presenting them as one number obscures the most important cost driver.

**Fix**: Replace single "Total Input Tokens" with a split display showing uncached and cache-read counts separately in the headline, or show a compact inline summary like `1 input + 107,193 cached`.

---

### 3. Cost column shows "-" with no explanation

**Location**: `internal/web/templates/fragments/log_rows.html`

Many rows in the logs table show `-` in the Cost column. No tooltip, footnote, or explanation exists. Users can't tell if it means free, unknown pricing, or an error.

**Fix**: Show "n/a" with a `title` attribute like `title="No pricing data available for this model"`, or use a muted-color label like `(unknown)`.

---

## Moderate Issues

### 4. Disclosure arrows suggest navigation, not expansion

**Location**: `internal/web/templates/log_detail.html`, `internal/web/templates/fragments/conv_viewer.html`

`Token Details >` and `Full arguments >` use Pico CSS's default right-pointing chevron, which universally means "navigate somewhere." The native `<details>` triangle rotates to indicate open/closed state, but the custom `token-details-summary` styling overrides this.

**Fix**: Use `▶`/`▼` rotation via CSS on `summary::marker` or `summary::before`, or simply rely on the browser-native disclosure triangle instead of overriding it.

---

### 5. Model filter is free-text, not a dropdown

**Location**: `internal/web/templates/logs.html` filter form

Users have to know and type the exact model name string (e.g., `cm/claude-sonnet-4-6`). The system already knows which models have been used.

**Fix**: Add a store query for distinct model names, populate a `<select>` dropdown (with an "All models" default option).

---

### 6. "Key" filter placeholder looks pre-filled

**Location**: `internal/web/templates/logs.html` filter form

The placeholder `llmp_sk_...` mimics actual key prefix format, making the field look like it already has a value selected.

**Fix**: Change placeholder to `Filter by key prefix…` and rename label to "Key Prefix" so intent is clear.

---

### 7. No visible indicator of active filters

**Location**: `internal/web/templates/logs.html`

Once you apply a filter, there's no visual indication the results are filtered. No active-filter chip, no highlighted filter bar, no reset button. If auto-refresh fires or you scroll, you won't know filters are active.

**Fix**: After filtering, show a "Filtered" badge or highlight the active filter fields. Add a "Clear filters" link that resets all fields and reloads.

---

### 8. Log rows aren't clickable — only a small "View" link

**Location**: `internal/web/templates/fragments/log_rows.html`

The entire row should link to the detail page. A 4-character "View" link on the far right of a wide table is a tiny click target and requires horizontal scrolling on smaller screens.

**Fix**: Make the entire `<tr>` clickable via a JavaScript click handler or wrap the first column (timestamp) in an `<a>` tag that's full-width. Keep the explicit "View" link as a fallback.

---

### 9. Cost bar graph is useless for small values

**Location**: `internal/web/templates/costs.html`

On the Costs page breakdown table, `claude-haiku-4-5` ($0.51 vs $20.69 for sonnet) renders its cost bar as a single pixel. Provides no visual information at all.

**Fix**: Set a minimum bar width (e.g., 2-3% of max) so all non-zero entries are visible.

---

### 10. Zero-token requests show $0.0000 with no explanation

**Location**: `internal/web/templates/costs.html` model breakdown table

`claude-sonnet-4.6` shows 4 requests with all token counts at 0 and $0.0000. These appear to be requests logged before the cache token migration, or failed requests where tokens weren't captured.

**Fix**: Either exclude zero-token rows from the breakdown, or add a footnote explaining "N requests had incomplete token data."

---

## Minor Issues

### 11. Timestamps lack timezone on logs list

**Location**: `internal/web/templates/fragments/log_rows.html`

Log list shows `Mar 11 15:41:41` but the detail page says `2026-03-11 15:04:05 UTC`. The list omits both year and timezone.

**Fix**: Append "UTC" to the timestamp format in the list, or add a subtle `(UTC)` label to the "Time" column header.

---

### 12. "Updated just now" never counts up

**Location**: `internal/web/templates/layout.html` JavaScript

The auto-refresh status shows "Updated just now" but never transitions to "30s ago" or "1 min ago". After 60+ seconds between successful refreshes it still says "just now."

**Fix**: Add a `setInterval` that updates the text relative to `lastRefresh`. Show "Updated Xs ago" or "Updated Xm ago" for older timestamps.

---

### 13. Auto-refresh rate is undisclosed

**Location**: `internal/web/templates/logs.html`

The page refreshes every 10 seconds via HTMX (`hx-trigger="every 10s"`), but nothing tells the user this. The Pause button exists but without context.

**Fix**: Change "Auto-refreshing" to "Auto-refreshing (10s)" or show a subtle countdown indicator.

---

### 14. System Prompt collapsed with no preview

**Location**: `internal/web/templates/log_detail.html` lines 100–107

The collapsed System Prompt disclosure gives no hint of content length or topic. Users must click to learn if there's anything meaningful.

**Fix**: Add a character/token count or first ~80 characters as preview text after the summary label, e.g., `System Prompt (2,847 chars)`.

---

### 15. Latest Prompt with only a tool result lacks context

**Location**: `internal/web/templates/log_detail.html`, `internal/web/conv.go`

When the last user turn is a tool result (e.g., a list of file paths), the "Latest Prompt" section shows "**Tool result** / [content]" with no indication of which tool produced it. Users need to open Conversation History to understand the context.

**Fix**: For tool_result blocks, show the matched tool_use_id's tool name inline, e.g., "Tool result for `Glob`" — this requires a reverse lookup from the conversation history.

---

### 16. Daily Cost Trend chart is sparse with single-day data

**Location**: `internal/web/templates/costs.html`

The date range defaults to 30 days but if all requests are from one day, you get one bar in the center of a wide empty chart.

**Fix**: Auto-narrow the chart's rendered date range to the actual data span, or hide the chart entirely when there's only a single data point and just show the summary stats.

---

### 17. No "Keys" management in the nav

**Location**: Navigation (`internal/web/templates/layout.html`)

The CLI has a `keys` command for managing proxy keys, but there's no web UI for viewing, creating, or revoking keys. The nav only shows Logs and Costs.

**Fix**: Add a "Keys" page to the web UI (or at minimum, a read-only list of active keys with their labels and creation dates). This is a larger feature addition and may warrant its own spec.

---

## Summary

| Severity | Count | Examples |
|----------|-------|---------|
| Critical | 3 | Root 404, misleading token headline, unexplained "-" cost |
| Moderate | 7 | Disclosure arrows, free-text model filter, non-clickable rows |
| Minor | 7 | Timezone, stale refresh indicator, sparse chart |

**Total**: 17 issues identified
