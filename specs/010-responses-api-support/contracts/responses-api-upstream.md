# Contract: Responses API Upstream (Provider-Facing)

**Upstream Endpoint**: `POST {base_url}/v1/responses`
**Authentication**: `Authorization: Bearer <api-key>` (proxy_key or forwarded)
**Provider APIFormat**: `"responses"`

## How the Proxy Sends Requests

When a provider is configured with `api_type: responses`, the proxy sends requests to `{base_url}/v1/responses` in Responses API format. The request body is either:

1. **Passthrough**: If the client sent a Responses API request, forward as-is (with model name remapped)
2. **Translated**: If the client sent Anthropic or Chat Completions format, translate to Responses API format

## Request Translation: Anthropic → Responses API

| Anthropic Field | Responses API Field |
|----------------|-------------------|
| system (string or blocks) | instructions |
| messages[].role | input[].role (in message items) |
| messages[].content (string) | input[].content[].type = "input_text" |
| messages[].content (image blocks) | input[].content[].type = "input_image" |
| messages[].content (tool_result blocks) | input[].type = "function_call_output" |
| messages[].content (tool_use blocks) | input[].type = "function_call" |
| max_tokens | max_output_tokens |
| temperature | temperature |
| top_p | top_p |
| stop_sequences | — (dropped, no direct equivalent) |
| tools[].name, .description, .input_schema | tools[].name, .description, .parameters |
| tool_choice {type: "auto"} | tool_choice "auto" |
| tool_choice {type: "any"} | tool_choice "required" |
| tool_choice {type: "tool", name} | tool_choice {type: "function", name} |
| stream | stream |

## Request Translation: Chat Completions → Responses API

| Chat Completions Field | Responses API Field |
|-----------------------|-------------------|
| messages[role: "system"].content | instructions |
| messages[role: "user"].content (string) | input[].content[].type = "input_text" |
| messages[role: "user"].content (image_url parts) | input[].content[].type = "input_image" |
| messages[role: "assistant"].content | input[].role = "assistant", content |
| messages[role: "assistant"].tool_calls | input[].type = "function_call" items |
| messages[role: "tool"].content | input[].type = "function_call_output" |
| max_tokens | max_output_tokens |
| temperature | temperature |
| top_p | top_p |
| stop | — (dropped, no direct equivalent) |
| tools[].function.name, .description, .parameters | tools[].name, .description, .parameters |
| tool_choice "auto" | tool_choice "auto" |
| tool_choice "required" | tool_choice "required" |
| tool_choice {type: "function", function: {name}} | tool_choice {type: "function", name} |
| stream | stream |
| response_format {type: "json_schema", ...} | text {format: {type: "json_schema", ...}} |

## Response Translation: Responses API → Anthropic

| Responses API Field | Anthropic Field |
|-------------------|----------------|
| output[type: "message"].content[type: "output_text"].text | content[].type = "text", .text |
| output[type: "message"].content[type: "refusal"].refusal | content[].type = "text", .text (refusal as text) |
| output[type: "function_call"] | content[].type = "tool_use", .id, .name, .input |
| status: "completed" | stop_reason: "end_turn" |
| status: "incomplete" (max_output_tokens) | stop_reason: "max_tokens" |
| output contains function_call | stop_reason: "tool_use" |
| usage.input_tokens | usage.input_tokens |
| usage.output_tokens | usage.output_tokens |
| usage.input_tokens_details.cached_tokens | usage.cache_read_input_tokens |
| id | id |

## Response Translation: Responses API → Chat Completions

| Responses API Field | Chat Completions Field |
|-------------------|----------------------|
| output[type: "message"].content[type: "output_text"].text | choices[0].message.content |
| output[type: "message"].content[type: "refusal"].refusal | choices[0].message.refusal |
| output[type: "function_call"] | choices[0].message.tool_calls[] |
| status: "completed" | choices[0].finish_reason: "stop" |
| status: "incomplete" (max_output_tokens) | choices[0].finish_reason: "length" |
| output contains function_call | choices[0].finish_reason: "tool_calls" |
| usage.input_tokens | usage.prompt_tokens |
| usage.output_tokens | usage.completion_tokens |
| usage.total_tokens | usage.total_tokens |
| id | id (prefixed "chatcmpl-") |
| object: "response" | object: "chat.completion" |

## Streaming Event Relay

When the upstream returns streaming Responses API events, the proxy:

1. **Passthrough** (client is Responses API): Forward all events unchanged, extract tokens from `response.completed`
2. **Translate to Anthropic SSE**: Map typed events to Anthropic's structured SSE format
3. **Translate to Chat Completions SSE**: Map typed events to Chat Completions delta format

### Token Extraction Points

| Stream Format | Token Source Event |
|--------------|-------------------|
| Responses API | `response.completed` → `response.usage` |
| Anthropic | `message_start` (input) + `message_delta` (output) |
| Chat Completions | Final chunk with `usage` field (when stream_options.include_usage is set) |

## Error Handling

Upstream Responses API errors are mapped to the client's expected error format:

| Responses API Error | Anthropic Error | Chat Completions Error |
|--------------------|-----------------|----------------------|
| 401 (authentication) | 401 authentication_error | 401 invalid_api_key |
| 400 (invalid request) | 400 invalid_request_error | 400 invalid_request_error |
| 429 (rate limit) | 429 rate_limit_error | 429 rate_limit_exceeded |
| 500+ (server error) | 500 api_error | 500 server_error |
