# Research: UI Log Improvements

**Feature**: 004-ui-log-improvements
**Date**: 2026-03-11

---

## 1. HTMX 2.x Polling with Filter Preservation

**Decision**: Use `hx-trigger="every 10s"` on the `<tbody id="log-table-body">` element with `hx-include="#filter-form"` to include live filter values on each poll. The `hx-get` URL carries the current page number via a data attribute.

**Pattern**:
```html
<tbody
  id="log-table-body"
  hx-get="/ui/api/logs"
  hx-trigger="every 10s"
  hx-include="#filter-form"
  hx-swap="innerHTML"
  hx-vals='{"page": "{{.Page}}"}'
  hx-on="htmx:afterRequest: updateRefreshStatus(event); htmx:responseError: markRefreshFailed()"
>
```

HTMX 2.x fires the `every Ns` trigger as a polling interval. `hx-include` with a CSS selector serialises all named inputs within the matched element and merges them into the request. `hx-vals` injects additional static key/value pairs (the current page).

**Filter form protection**: The filter form must have `id="filter-form"` and NOT itself have `hx-trigger="every"` — polling lives on the `<tbody>`, not the form. This avoids resetting form fields on auto-refresh because the form values are read at poll time, not replaced.

**Rationale**: This is the standard HTMX polling pattern. An alternative of polling the whole page and using `hx-select` was considered but discarded as it replaces more DOM than needed.

---

## 2. Last-Updated Status Indicator with Failure Detection

**Decision**: Use inline JavaScript via `hx-on:htmx:after-request` and `hx-on:htmx:response-error` attributes on the `<tbody>` to update a status `<span>` outside the swap target.

**Pattern**:
```html
<tbody
  id="log-table-body"
  hx-get="/ui/api/logs"
  hx-trigger="every 10s"
  hx-include="#filter-form"
  hx-swap="innerHTML"
  hx-on:htmx:after-request="updateRefreshStatus(event)"
  hx-on:htmx:response-error="markRefreshFailed()"
>
```

```html
<span id="refresh-status" class="refresh-status">Auto-refreshing</span>
```

```js
let lastRefresh = null;
function updateRefreshStatus(evt) {
  if (evt.detail.successful) {
    lastRefresh = new Date();
    const el = document.getElementById('refresh-status');
    el.textContent = 'Updated just now';
    el.className = 'refresh-status';
    // Check X-New-Count header for banner
    const newCount = evt.detail.xhr.getResponseHeader('X-New-Count');
    if (newCount && parseInt(newCount) > 0) {
      showNewEntriesBanner(parseInt(newCount));
    }
  } else {
    markRefreshFailed();
  }
}
function markRefreshFailed() {
  const el = document.getElementById('refresh-status');
  el.textContent = 'Refresh failed';
  el.className = 'refresh-status refresh-stale';
}
```

**Rationale**: HTMX 2.x `hx-on:*` syntax directly binds JavaScript to HTMX lifecycle events on the element itself. This avoids a global `document.addEventListener` and keeps behavior co-located with the element. The `htmx:after-request` event carries `detail.successful` (boolean) and `detail.xhr` for header access.

---

## 3. New Entries Banner for Page 2+

**Decision**: Add an `X-New-Count` response header to the `/ui/api/logs` HTMX fragment response when the request includes a `since_id` parameter. Read this header in the `htmx:after-request` event handler to conditionally show/hide a dismissible banner.

**Server change**: When `since_id` query param is present, `LogsAPIHandler` computes how many entries exist with an ID/timestamp newer than `since_id` and includes `X-New-Count: N` in the response header.

**Store change**: Add `CountLogsSince(ctx, sinceID string) (int64, error)` to the store interface.

**Banner HTML** (initially hidden):
```html
<div id="new-entries-banner" style="display:none" role="alert">
  <span id="new-entries-count"></span> new entries —
  <a href="/ui/logs">view latest</a>
  <button onclick="document.getElementById('new-entries-banner').style.display='none'">×</button>
</div>
```

**JavaScript**:
```js
function showNewEntriesBanner(count) {
  const banner = document.getElementById('new-entries-banner');
  document.getElementById('new-entries-count').textContent = count;
  banner.style.display = '';
}
```

**Rationale**: This avoids a separate polling endpoint. The existing HTMX poll piggybacks the new-count check. Only active on page 2+ (the Go template conditionally adds `since_id` to `hx-vals` when `Page > 1` using the ID of the first log entry in the current view).

---

## 4. Pico CSS v2 Dark Mode

**Decision**: Remove the `data-theme="light"` attribute from `<html>` (or change to `data-theme="auto"`).

Pico CSS v2 automatically applies `prefers-color-scheme` dark/light selection when the `data-theme` attribute is absent or set to `"auto"`. The current value `"light"` forces light mode and prevents OS-preference detection. Both removing the attribute and using `data-theme="auto"` produce identical behaviour.

**Custom properties for warning color**: Use `--pico-form-element-warning-border-color` for the staleness indicator — this is the correct Pico v2 semantic warning variable (used for form validation warnings). The `--pico-ins-color` (green) and `--pico-del-color` (red) variables already used in the codebase complete the traffic-light set:
```css
.refresh-stale { color: var(--pico-form-element-warning-border-color, #f59e0b); }
```

**Rationale**: Single-attribute change. No other template modifications needed for dark mode support. Pico v2 handles all element-level theming automatically.

---

## 5. Clipboard API with Fallback

**Decision**: Use `navigator.clipboard.writeText()` with `window.isSecureContext` guard and `execCommand` fallback.

**Pattern** (inline on button):
```html
<button
  class="outline secondary copy-btn"
  onclick="copyText(this, '{{.Log.ID}}')"
  title="Copy ID"
>⎘</button>
```

```js
function copyText(btn, text) {
  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(text).then(function() {
      btn.textContent = '✓';
      setTimeout(function() { btn.textContent = '⎘'; }, 1500);
    });
  } else {
    // Fallback: select a temporary textarea
    const el = document.createElement('textarea');
    el.value = text;
    el.style.position = 'fixed';
    el.style.opacity = '0';
    document.body.appendChild(el);
    el.select();
    document.execCommand('copy');
    document.body.removeChild(el);
    btn.textContent = '✓';
    setTimeout(function() { btn.textContent = '⎘'; }, 1500);
  }
}
```

**Rationale**: `navigator.clipboard` requires a secure context (HTTPS or localhost). The `execCommand('copy')` fallback is deprecated but has near-universal browser support and covers the non-HTTPS dev scenario. Brief visual feedback (`✓` for 1.5s) confirms the copy without a tooltip library.

---

## 6. Conversation Body Parsing

**Decision**: Parse `RequestBody` in the Go handler using `json.Unmarshal` into `anthropic.MessagesRequest`. The `System` field is `interface{}` — handle three cases: `string`, `[]interface{}` (array of blocks), and `nil`. Normalise all to a `string` for the template (for array system prompts, join text blocks; indicate non-text blocks with `[<type> block]`).

**Tool result matching**: Tool-use blocks in assistant turns carry an `id` field. Tool-result blocks in subsequent user turns carry a `tool_use_id` that matches. Build a lookup map `map[string]string` (toolUseID → result content) during the message parse pass and attach results to the corresponding `ConvBlock`.

**Truncation**: Apply at the `ConvBlock` level: if `len(text) > 500`, set `Truncated=true`, `Text=text[:500]`, `FullText=text`. The template renders a `<details>` expand for truncated blocks.

**Fallback**: If `json.Unmarshal` fails or the resulting `Messages` slice is nil/empty, set `ParseFailed=true` and pass the original `RequestBody` for pretty-printing via `json.MarshalIndent`.

**Rationale**: Parsing in Go (not JavaScript) keeps the template logic simple and makes the parsing testable. All template data is pre-computed structs, not raw JSON strings for the conversation view.

---

## 7. Cost Breakdown at Display Time

**Decision**: Pass a reference to `*pricing.Calculator` to `Handlers` (add to the struct). In `LogDetailPage`, call a new `computeCostBreakdown(log, calc)` helper that returns a `*CostBreakdown` struct containing per-category costs.

**Pricing formula** (mirrors `Calculator.Calculate`):
- Input cost: `inputTokens * InputPerMillion / 1_000_000`
- Cache write cost: `cacheCreationTokens * CacheWritePerMillion / 1_000_000`
- Cache read cost: `cacheReadTokens * CacheReadPerMillion / 1_000_000`
- Output cost: `outputTokens * OutputPerMillion / 1_000_000`

If the model is not in the pricing table, `PricingKnown=false` and all cost fields are `nil`.

**Rationale**: No new DB columns or migrations needed. The pricing table is already loaded at startup. Computing at render time is negligible overhead for a single detail page view.

---

## Alternatives Considered

| Decision | Alternative | Rejected Because |
|----------|-------------|-----------------|
| Conversation parsing in Go handler | Parse JSON in JavaScript on the frontend | Go parsing is testable, keeps templates simple, no JS bundle needed |
| `X-New-Count` header on existing poll | Separate `/ui/api/logs/count` endpoint | Avoids extra HTTP round-trip; reuses existing poll interval |
| `hx-trigger="every 10s"` on `<tbody>` | Polling wrapper `<div>` with `hx-select` | Direct tbody polling is simpler and replaces less DOM |
| Cost breakdown computed at render time | Store per-category costs in new DB columns | No migration needed; pricing table already in memory at startup |
| `data-theme="auto"` (Pico v2) | Media query CSS override in `<style>` | Single attribute change; Pico v2 handles all element theming automatically |
