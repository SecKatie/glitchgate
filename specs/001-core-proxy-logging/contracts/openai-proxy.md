# Contract: OpenAI-Compatible Proxy Endpoint

The proxy exposes an OpenAI Chat Completions API-compatible endpoint.
Requests are translated to the upstream provider's native format
(Anthropic for MVP), forwarded, and responses are translated back
to OpenAI format.

## Endpoint

```
POST /v1/chat/completions
```

## Request Headers

| Header | Required | Description |
|--------|----------|-------------|
| `authorization` | Yes | `Bearer <proxy-api-key>` for proxy authentication |
| `x-proxy-api-key` | Alternative | Alternative header for proxy API key (when Authorization is needed for credential forwarding) |
| `content-type` | Yes | Must be `application/json` |

## Request Body

```json
{
  "model": "claude-sonnet",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "Hello, world"}
  ],
  "max_tokens": 1024,
  "temperature": 0.7,
  "stream": false
}
```

**Translation to Anthropic format**:
- `messages` with `role: "system"` → extracted to Anthropic `system` field
- `messages` with `role: "user"` / `role: "assistant"` → mapped directly
- `max_tokens` → `max_tokens`
- `temperature` → `temperature`
- `model` → resolved via model mapping, then to upstream model name

## Response (non-streaming)

Translated from Anthropic response format to OpenAI format.

```json
{
  "id": "chatcmpl-msg_01XFDUDYJgAACzvnptvVoYEL",
  "object": "chat.completion",
  "created": 1710000000,
  "model": "claude-sonnet",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "Hello! How can I help you today?"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 25,
    "completion_tokens": 12,
    "total_tokens": 37
  }
}
```

**Translation from Anthropic**:
- `stop_reason: "end_turn"` → `finish_reason: "stop"`
- `stop_reason: "max_tokens"` → `finish_reason: "length"`
- `stop_reason: "stop_sequence"` → `finish_reason: "stop"`
- `usage.input_tokens` → `usage.prompt_tokens`
- `usage.output_tokens` → `usage.completion_tokens`
- `usage.total_tokens` = sum of prompt + completion

## Response (streaming)

When `"stream": true`, the response is SSE in OpenAI chunk format.

```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
```

```
data: {"id":"chatcmpl-msg_...","object":"chat.completion.chunk","created":1710000000,"model":"claude-sonnet","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"chatcmpl-msg_...","object":"chat.completion.chunk","created":1710000000,"model":"claude-sonnet","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-msg_...","object":"chat.completion.chunk","created":1710000000,"model":"claude-sonnet","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

**Streaming translation from Anthropic events**:
- `message_start` → initial chunk with `delta.role`
- `content_block_delta` → chunk with `delta.content`
- `message_delta` (with stop_reason) → final chunk with `finish_reason`
- `message_stop` → `data: [DONE]`

## Error Responses

Proxy-level errors use OpenAI error format:

```json
{
  "error": {
    "message": "Invalid proxy API key",
    "type": "authentication_error",
    "code": "invalid_api_key"
  }
}
```

| Status | Condition |
|--------|-----------|
| 401 | Invalid or missing proxy API key |
| 400 | Malformed request body or unknown model name |
| 502 | Upstream provider unreachable |
| 504 | Upstream provider timeout |

## Logging

Same as Anthropic endpoint. The log entry records:
- `source_format: "openai"` to indicate the request arrived in
  OpenAI format
- Both the original OpenAI request and the translated response
- Token counts and cost calculated identically
