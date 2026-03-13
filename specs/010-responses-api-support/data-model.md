# Data Model: OpenAI Responses API Support

**Feature**: 010-responses-api-support
**Date**: 2026-03-12

## Entities

### ResponsesRequest

The top-level request structure for the `/v1/responses` endpoint.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| model | string | Yes | Model identifier (e.g., "gpt-4o", "o3") |
| input | string \| []InputItem | No | Text prompt or array of structured input items |
| instructions | *string | No | System-level instructions (separate from input) |
| temperature | *float64 | No | Sampling temperature (0-2) |
| top_p | *float64 | No | Nucleus sampling parameter |
| max_output_tokens | *int | No | Upper bound on output tokens |
| stream | *bool | No | Enable SSE streaming |
| store | *bool | No | Persist response server-side (default true) |
| previous_response_id | *string | No | Chain to previous response for conversation state |
| metadata | map[string]string | No | Key-value metadata (max 16 pairs) |
| tools | []ResponsesTool | No | Tool definitions |
| tool_choice | interface{} | No | "none" \| "auto" \| "required" \| object |
| parallel_tool_calls | *bool | No | Allow parallel tool invocations |
| truncation | *string | No | "auto" \| "disabled" |
| reasoning | *Reasoning | No | {effort, summary} for reasoning models |
| text | *TextConfig | No | Structured output format configuration |
| include | []string | No | Extra output data to include |
| background | *bool | No | Run asynchronously |
| service_tier | *string | No | Processing tier selection |

**Notes**: `input` uses `json.RawMessage` for polymorphic parsing (string vs array). Behavioral parameters (temperature, top_p, truncation, reasoning, etc.) are classified as "optional" per FR-017 — silently dropped or mapped when translating to other formats.

### InputItem (Union Type)

Discriminated by `type` field. Represents one element in the `input` array.

| Type Value | Fields | Description |
|-----------|--------|-------------|
| input_text | text | Plain text content |
| input_image | image_url, detail, file_id | Image via URL, base64 data URI, or file reference |
| input_file | file_id, file_data, file_url, filename | Document (PDF, code, etc.) |
| input_audio | data, format | Audio content |
| message | role, content ([]InputItem) | Structured message with role |
| function_call | call_id, name, arguments | Previous function call (for multi-turn) |
| function_call_output | call_id, output | Result of a function call |
| item_reference | id | Reference to stored item |

**Notes**: `message` type with `role` field is the "easy input" format — equivalent to Chat Completions messages but using Responses API content types. `item_reference` is Responses-only (no translation to other formats).

### ResponsesTool

Tool definition in Responses API flat format.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | Yes | "function", "web_search", "file_search", "code_interpreter", etc. |
| name | string | Yes (function) | Function name |
| description | string | No | Function description |
| parameters | json.RawMessage | No | JSON Schema for function parameters |
| strict | *bool | No | Enable strict schema mode (default true) |

**Notes**: Built-in tools (web_search, file_search, code_interpreter) are passthrough-only to Responses API upstreams. Function tools translate to/from other format tool definitions.

### ResponsesResponse

The response structure returned by the Responses API.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| id | string | Yes | Unique response ID (e.g., "resp_...") |
| object | string | Yes | Always "response" |
| created_at | float64 | Yes | Unix timestamp |
| model | string | Yes | Model that generated the response |
| status | string | Yes | "completed" \| "failed" \| "incomplete" \| "in_progress" |
| output | []OutputItem | Yes | Array of typed output items |
| usage | *ResponsesUsage | No | Token usage details |
| error | *ResponsesError | No | Error details if status is "failed" |
| incomplete_details | *IncompleteDetails | No | Reason for incomplete status |
| instructions | *string | No | Instructions echo |
| metadata | map[string]string | No | Metadata echo |
| temperature | float64 | No | Temperature used |
| top_p | float64 | No | Top_p used |
| tool_choice | interface{} | No | Tool choice setting echo |
| tools | []ResponsesTool | No | Tools available echo |

### OutputItem (Union Type)

Discriminated by `type` field. Represents one element in the `output` array.

| Type Value | Key Fields | Description |
|-----------|------------|-------------|
| message | id, role, content ([]OutputContent), status | Assistant message with content parts |
| function_call | id, call_id, name, arguments, status | Function call invocation |
| file_search_call | id, queries, results, status | Built-in file search result |
| web_search_call | id, action, status | Built-in web search result |
| code_interpreter_call | id, code, results, status | Built-in code interpreter result |
| reasoning | id, summary | Reasoning trace (if included) |

### OutputContent (Union Type)

Content parts within a message OutputItem.

| Type Value | Fields | Description |
|-----------|--------|-------------|
| output_text | text, annotations | Text output with optional annotations |
| refusal | refusal | Model refusal message |

### ResponsesUsage

Token accounting for Responses API responses.

| Field | Type | Description |
|-------|------|-------------|
| input_tokens | int | Input token count |
| input_tokens_details | InputTokensDetails | {cached_tokens} |
| output_tokens | int | Output token count |
| output_tokens_details | OutputTokensDetails | {reasoning_tokens} |
| total_tokens | int | Total tokens |

**Mapping to existing proxy token fields**:
- `input_tokens` → `provider.Response.InputTokens`
- `output_tokens` → `provider.Response.OutputTokens`
- `input_tokens_details.cached_tokens` → `provider.Response.CacheReadInputTokens`

### OpenAIProviderConfig

Extension to existing `ProviderConfig` for OpenAI providers.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| name | string | Yes | Provider identifier |
| type | string | Yes | "openai" |
| base_url | string | Yes | Upstream endpoint base URL |
| auth_mode | string | Yes | "proxy_key" \| "forward" |
| api_key | string | Conditional | Required for proxy_key auth mode |
| api_type | string | Yes | "chat_completions" \| "responses" |

## Translation Mappings

### Content Type Mapping

| Responses API | Anthropic | Chat Completions |
|--------------|-----------|-----------------|
| input_text | text string or text block | {type: "text", text} |
| input_image (URL) | {type: "image", source: {type: "url", url}} | {type: "image_url", image_url: {url}} |
| input_image (base64) | {type: "image", source: {type: "base64", media_type, data}} | {type: "image_url", image_url: {url: "data:..."}} |
| input_file | ERROR — no equivalent | ERROR — no equivalent |
| input_audio | ERROR — no Anthropic equivalent | {type: "input_audio"} (provider-dependent) |
| output_text | text content block | message.content string |
| refusal | text content block (with refusal text) | message.refusal string |

### Message Role Mapping

| Responses API | Anthropic | Chat Completions |
|--------------|-----------|-----------------|
| message (role: "user") | user message | user message |
| message (role: "assistant") | assistant message | assistant message |
| instructions parameter | system field | system message |
| function_call (output) | tool_use content block | tool_calls[] |
| function_call_output (input) | tool_result content block | tool role message |

### Stop Reason Mapping

| Responses API (status + incomplete) | Anthropic | Chat Completions |
|-------------------------------------|-----------|-----------------|
| status: "completed" | end_turn | stop |
| status: "incomplete", reason: "max_output_tokens" | max_tokens | length |
| output contains function_call | tool_use | tool_calls |
| status: "incomplete", reason: "content_filter" | — | content_filter |

### Token Usage Mapping

| Responses API | Anthropic | Chat Completions |
|--------------|-----------|-----------------|
| usage.input_tokens | usage.input_tokens | usage.prompt_tokens |
| usage.output_tokens | usage.output_tokens | usage.completion_tokens |
| usage.total_tokens | (computed) | usage.total_tokens |
| input_tokens_details.cached_tokens | usage.cache_read_input_tokens | prompt_tokens_details.cached_tokens |

### Streaming Event Mapping

| Responses API Event | Anthropic Event | Chat Completions Event |
|--------------------|-----------------|----------------------|
| response.created | message_start | First chunk (id, model, role) |
| response.output_text.delta | content_block_delta (text_delta) | choices[0].delta.content |
| response.function_call_arguments.delta | content_block_delta (input_json_delta) | choices[0].delta.tool_calls[0].function.arguments |
| response.output_item.added | content_block_start | — (implicit) |
| response.output_item.done | content_block_stop | — (implicit) |
| response.completed | message_delta + message_stop | Final chunk with finish_reason + usage |
| response.failed | error event | error event |

## State Transitions

### Response Status Lifecycle

```
in_progress → completed     (normal completion)
in_progress → incomplete    (max tokens, content filter)
in_progress → failed        (error)
queued → in_progress        (background mode only)
```

The proxy only observes final states (`completed`, `incomplete`, `failed`) in synchronous mode. For streaming, the proxy relays status events as they arrive.

## Validation Rules

- `model` is required and must resolve to a configured model mapping
- `input` must be valid JSON (string or array of InputItem)
- `tools` array must contain valid tool definitions with unique names
- `temperature` must be 0-2 if provided
- `max_output_tokens` must be positive if provided
- Content-bearing fields validated before translation; behavioral parameters validated by upstream
