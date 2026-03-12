# Contract: Anthropic-Compatible Proxy Endpoint

The proxy exposes an Anthropic Messages API-compatible endpoint that
accepts requests in the same format as `https://api.anthropic.com`.

## Endpoint

```
POST /v1/messages
```

## Request Headers

| Header | Required | Description |
|--------|----------|-------------|
| `x-api-key` or `x-proxy-api-key` | Yes | Proxy API key for authentication |
| `authorization` | Conditional | Bearer token; forwarded to upstream when provider auth_mode=forward |
| `anthropic-version` | No | API version string; forwarded to upstream (default from provider config) |
| `content-type` | Yes | Must be `application/json` |

**Auth mode behavior**:
- `proxy_key` mode: The proxy authenticates the client via `x-api-key`
  (or `x-proxy-api-key`), then uses its own configured API key for the
  upstream request.
- `forward` mode: The proxy authenticates the client via
  `x-proxy-api-key`, then forwards the client's `authorization` header
  to the upstream.

## Request Body

Passed through to the upstream provider after model name resolution.

```json
{
  "model": "claude-sonnet",
  "max_tokens": 1024,
  "messages": [
    {"role": "user", "content": "Hello, world"}
  ],
  "stream": false,
  "system": "You are a helpful assistant."
}
```

The `model` field is resolved via model mapping configuration:
`"claude-sonnet"` → provider `"anthropic"`, upstream model
`"claude-sonnet-4-20250514"`.

## Response (non-streaming)

Returned verbatim from the upstream provider.

```json
{
  "id": "msg_01XFDUDYJgAACzvnptvVoYEL",
  "type": "message",
  "role": "assistant",
  "content": [
    {"type": "text", "text": "Hello! How can I help you today?"}
  ],
  "model": "claude-sonnet-4-20250514",
  "stop_reason": "end_turn",
  "usage": {
    "input_tokens": 25,
    "output_tokens": 12
  }
}
```

## Response (streaming)

When `"stream": true`, the response is forwarded as SSE events.

```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
```

Events are forwarded verbatim from the upstream. Key event types:

```
event: message_start
data: {"type":"message_start","message":{"id":"msg_...","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-20250514","usage":{"input_tokens":25,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":12}}

event: message_stop
data: {"type":"message_stop"}
```

## Error Responses

Upstream errors are forwarded with their original status code and body.
Proxy-level errors use the Anthropic error format:

```json
{
  "type": "error",
  "error": {
    "type": "authentication_error",
    "message": "Invalid proxy API key"
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

After each request completes (or stream ends), a `request_logs` entry
is created with:
- Redacted request body (API keys stripped)
- Full response body (reconstructed from SSE chunks for streaming)
- Token counts from upstream `usage` field
- Calculated cost based on model pricing config
