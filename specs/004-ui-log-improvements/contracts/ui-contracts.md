# UI Contracts: UI Log Improvements

**Feature**: 004-ui-log-improvements
**Date**: 2026-03-11

This document describes the web UI endpoints added or modified by this feature. All endpoints are protected by existing session cookie authentication.

---

## Modified Endpoints

### GET /ui/api/logs

**Purpose**: Returns the log table fragment (HTMX) or JSON. Extended to support new-entries count.

**New Query Parameter**:

| Parameter | Type | Description |
|-----------|------|-------------|
| `since_id` | string | Optional. When present, counts entries newer than this log ID (matching the active filter). |

**New Response Header** (HTMX requests only, when `since_id` is provided):

| Header | Value | Description |
|--------|-------|-------------|
| `X-New-Count` | integer string (e.g. `"3"`) | Number of entries newer than `since_id` that match the active filter. `"0"` when none. Only set when `since_id` is non-empty and page > 1. |

**Existing behaviour unchanged**: All existing query parameters (`page`, `per_page`, `model`, `status`, `key_prefix`, `from`, `to`, `sort`, `order`) continue to work as before.

**Error behaviour**: If `since_id` is provided but not found in the database, `X-New-Count` is set to `"0"` (not an error).

---

## New Endpoints

### GET /ui/logs/:id (updated response, same route)

The existing log detail page handler is updated to pass additional data to the template. The route and HTTP method are unchanged.

**Template data additions**:

| Field | Type | Description |
|-------|------|-------------|
| `Conversation` | `*ConversationData` | Parsed conversation structure. `nil` only if store lookup fails. |
| `Cost` | `*CostBreakdown` | Per-category token cost breakdown. `Cost.PricingKnown=false` when model not in pricing table. |

**No new HTTP endpoint is created.** The existing `GET /ui/logs/:id` route serves the enhanced detail page.

---

## Template Fragment Contracts

### log_rows (fragment template)

Called by: `GET /ui/api/logs` (HTMX requests)
Target: `#log-table-body`

**Data shape** (unchanged):
```
.Logs      []store.RequestLogSummary
.Page      int
.TotalPages int
```

**Behaviour change**: The `<tbody>` element in `logs.html` now carries HTMX polling attributes and new-entries banner attributes. The fragment itself (`log_rows`) is unchanged in structure.

---

## HTMX Polling Protocol

The logs page `<tbody>` element uses the following attributes to drive auto-refresh:

```
hx-get="/ui/api/logs"
hx-trigger="every 10s"
hx-include="#filter-form"
hx-swap="innerHTML"
hx-vals='{"page":"<current page>","since_id":"<first entry ID on page 1>"}'
```

- `since_id` is only included in `hx-vals` when `Page > 1` (set server-side in the template).
- When auto-refresh is paused (user toggles off), the `hx-trigger` attribute is removed from the element via JavaScript; re-enabling adds it back.
- The `htmx:after-request` lifecycle event is used to update the "Last updated" status indicator and read the `X-New-Count` response header.

---

## Security Notes

- All UI routes remain behind the existing session cookie middleware. No new authentication surface.
- The `since_id` parameter is an opaque log ID string; the store implementation MUST treat it as untrusted input and use parameterised queries.
- The `X-New-Count` header contains only an integer; no user data is exposed.
