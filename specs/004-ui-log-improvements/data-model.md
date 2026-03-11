# Data Model: UI Log Improvements

**Feature**: 004-ui-log-improvements
**Date**: 2026-03-11

---

## Overview

This feature requires no database schema changes. All new data structures are Go types used exclusively within `internal/web/` to pass pre-computed view data to HTML templates. The existing `store.RequestLogDetail` and `store.RequestLogSummary` types are unchanged.

One new store method is added to support the new-entries banner.

---

## New Go Types (internal/web/)

### ConversationData

Holds the parsed view of a request body's conversation structure. Passed to `log_detail.html`.

```go
type ConversationData struct {
    SystemPrompt string    // normalised from MessagesRequest.System; empty if absent
    HasSystem    bool      // true if a system prompt is present

    LatestPrompt *ConvTurn // the last user-role message; nil if messages is empty
    Response     *ConvTurn // parsed from ResponseBody; nil if parse failed
    History      []ConvTurn // all turns before LatestPrompt, oldest first

    ParseFailed  bool   // true if RequestBody could not be parsed as MessagesRequest
    RawRequest   string // pretty-printed RequestBody (always populated)
    RawResponse  string // pretty-printed ResponseBody (always populated)
}
```

### ConvTurn

A single conversation turn for template rendering.

```go
type ConvTurn struct {
    Role   string     // "user", "assistant", "system"
    Blocks []ConvBlock
}
```

### ConvBlock

A typed content block within a turn.

```go
type ConvBlock struct {
    // Type is one of: "text", "tool_use", "tool_result", "image", "document", "unknown"
    Type string

    // For Type="text"
    Text      string
    Truncated bool   // true if Text was truncated to ~500 chars
    FullText  string // complete text when Truncated=true (for "Show more" expand)

    // For Type="tool_use"
    ToolName  string // e.g. "get_weather"
    ToolInput string // pretty-printed JSON of input arguments
    ToolID    string // tool_use id for cross-turn matching

    // For Type="tool_result"
    ToolUseID     string // matches a prior ToolID
    ResultContent string // tool result text content (truncated if long)
    ResultTrunc   bool   // true if ResultContent was truncated

    // For Type="image" or "document" — label only, no raw data
    MediaLabel string // e.g. "[image/jpeg]" or "[application/pdf]"
}
```

### CostBreakdown

Per-category cost data for the detail page's Token Details section.

```go
type CostBreakdown struct {
    PricingKnown bool // false if model not in pricing table

    InputTokens       int64
    CacheWriteTokens  int64
    CacheReadTokens   int64
    OutputTokens      int64

    InputCostUSD      *float64 // nil when PricingKnown=false
    CacheWriteCostUSD *float64
    CacheReadCostUSD  *float64
    OutputCostUSD     *float64
    TotalCostUSD      *float64 // sum of above; matches stored EstimatedCostUSD
}
```

### LogDetailData

The top-level struct passed to `log_detail.html`.

```go
type LogDetailData struct {
    ActiveTab    string
    Log          *store.RequestLogDetail
    Conversation *ConversationData
    Cost         *CostBreakdown
}
```

---

## Store Interface Addition

One new method is added to `store.Store` to support the new-entries banner:

```go
// CountLogsSince returns the number of request log entries created after the
// entry with the given ID. Returns 0 if sinceID is empty or not found.
CountLogsSince(ctx context.Context, sinceID string, params ListLogsParams) (int64, error)
```

The `params` argument carries the active filter (model, status, key prefix, date range) so the count reflects only entries the user would actually see given their current filter.

**SQL sketch**:
```sql
SELECT COUNT(*)
FROM request_logs
WHERE timestamp > (SELECT timestamp FROM request_logs WHERE id = :since_id)
  AND (/* same filter conditions as ListRequestLogs */)
```

---

## Parsing Rules

### System Prompt Normalisation

`MessagesRequest.System` is `interface{}` (can be `string` or `[]map[string]interface{}`).

| JSON value | Normalised result |
|-----------|-------------------|
| `"You are a helpful assistant"` | `"You are a helpful assistant"` |
| `[{"type":"text","text":"..."}]` | concatenated text from all `type=text` blocks |
| `[{"type":"text","text":"..."}, {"type":"image_url","..."}]` | text blocks joined; non-text blocks replaced with `[image block]` |
| absent / `null` | empty string; `HasSystem=false` |

### Message Parsing Pass

1. Unmarshal `RequestBody` into `anthropic.MessagesRequest`. On error → `ParseFailed=true`, populate `RawRequest` only.
2. Walk `Messages` from index 0 to N-1:
   - Build `History` from all messages except the last user-role message.
   - Identify the last `role=user` message as `LatestPrompt`.
   - Build a `toolResults` map: `map[string]string` from tool-result blocks in user messages (keyed by `tool_use_id`).
3. Walk `Messages` again for assistant turns and attach matched tool results to `ToolID` blocks.
4. Unmarshal `ResponseBody` into `anthropic.MessagesResponse` for the `Response` turn. On error, `Response=nil`.

### Content String Truncation

Applied to `ConvBlock.Text`, `ConvBlock.FullText`, `ConvBlock.ToolInput`, `ConvBlock.ResultContent`:
- Threshold: 500 Unicode characters.
- If `len([]rune(s)) > 500`: `Text = string([]rune(s)[:500])`, `Truncated = true`, `FullText = s`.
- `ToolInput` (JSON): truncate at 500 characters; `FullText` holds the complete JSON.

---

## No Schema Changes

The existing `request_logs` table (with `cache_creation_input_tokens` and `cache_read_input_tokens` columns from migration `003`) already contains all data needed for the cost breakdown. No new migration is required.
