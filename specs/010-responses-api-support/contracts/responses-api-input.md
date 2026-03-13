# Contract: Responses API Input (Client-Facing)

**Endpoint**: `POST /v1/responses`
**Authentication**: Proxy key via `Authorization: Bearer <proxy-key>` header
**Content-Type**: `application/json`
**Source Format**: `"responses"` (logged in request_logs)

## Request

```json
{
  "model": "gpt-4o",
  "input": [
    {
      "role": "user",
      "content": [
        {"type": "input_text", "text": "What is in this image?"},
        {"type": "input_image", "image_url": "https://example.com/photo.jpg", "detail": "auto"}
      ]
    }
  ],
  "instructions": "You are a helpful assistant.",
  "tools": [
    {
      "type": "function",
      "name": "get_weather",
      "description": "Get weather for a location",
      "parameters": {
        "type": "object",
        "properties": {"location": {"type": "string"}},
        "required": ["location"]
      }
    }
  ],
  "temperature": 0.7,
  "max_output_tokens": 4096,
  "stream": false
}
```

### Request Fields

| Field | Type | Proxy Behavior |
|-------|------|---------------|
| model | string | **Required.** Resolved via model mapping to upstream provider. |
| input | string \| []InputItem | **Essential.** Translated to target format. String wrapped as single user message. |
| instructions | *string | **Essential.** Mapped to system message/field in target format. |
| tools | []Tool | **Essential.** Function tools translated; built-in tools passthrough to Responses upstreams only. |
| tool_choice | interface{} | **Essential.** Semantic mapping between formats. Responses-only values error on non-Responses upstreams. |
| temperature | *float64 | **Optional.** Passed through or dropped. |
| top_p | *float64 | **Optional.** Passed through or dropped. |
| max_output_tokens | *int | **Optional.** Mapped to max_tokens in other formats. |
| stream | *bool | **Essential.** Streaming supported across all format combinations. |
| store | *bool | **Optional.** Passthrough to Responses upstreams; dropped for others. |
| previous_response_id | *string | **Optional.** Passthrough to Responses upstreams; silently dropped for others. |
| metadata | map[string]string | **Optional.** Passthrough to Responses upstreams; dropped for others. |
| reasoning | *Reasoning | **Optional.** Passthrough or dropped. |
| truncation | *string | **Optional.** Passthrough or dropped. |
| text | *TextConfig | **Optional.** Mapped to response_format in Chat Completions; dropped for Anthropic. |
| background | *bool | **Optional.** Passthrough to Responses upstreams; dropped for others. |

## Non-Streaming Response

```json
{
  "id": "resp_abc123",
  "object": "response",
  "created_at": 1710288000.0,
  "model": "gpt-4o",
  "status": "completed",
  "output": [
    {
      "type": "message",
      "id": "msg_001",
      "role": "assistant",
      "content": [
        {
          "type": "output_text",
          "text": "The image shows a sunset over the ocean.",
          "annotations": []
        }
      ],
      "status": "completed"
    }
  ],
  "usage": {
    "input_tokens": 150,
    "output_tokens": 25,
    "total_tokens": 175,
    "input_tokens_details": {"cached_tokens": 0},
    "output_tokens_details": {"reasoning_tokens": 0}
  }
}
```

## Streaming Response

**Content-Type**: `text/event-stream`

```
event: response.created
data: {"type":"response.created","response":{"id":"resp_abc123","object":"response","status":"in_progress","model":"gpt-4o","output":[]}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_001","role":"assistant","content":[],"status":"in_progress"}}

event: response.content_part.added
data: {"type":"response.content_part.added","item_id":"msg_001","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_001","output_index":0,"content_index":0,"delta":"The image "}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_001","output_index":0,"content_index":0,"delta":"shows a sunset."}

event: response.output_text.done
data: {"type":"response.output_text.done","item_id":"msg_001","output_index":0,"content_index":0,"text":"The image shows a sunset."}

event: response.content_part.done
data: {"type":"response.content_part.done","item_id":"msg_001","output_index":0,"content_index":0,"part":{"type":"output_text","text":"The image shows a sunset.","annotations":[]}}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_001","role":"assistant","content":[{"type":"output_text","text":"The image shows a sunset.","annotations":[]}],"status":"completed"}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_abc123","object":"response","status":"completed","model":"gpt-4o","output":[...],"usage":{"input_tokens":150,"output_tokens":25,"total_tokens":175}}}

data: [DONE]
```

## Error Response

```json
{
  "id": "resp_err123",
  "object": "response",
  "status": "failed",
  "error": {
    "code": "unsupported_feature",
    "message": "input_file content type is not supported by the resolved upstream provider (Anthropic). Only text and image content are translatable."
  }
}
```

## Tool Use Flow

### Request with tool call result:

```json
{
  "model": "gpt-4o",
  "input": [
    {"role": "user", "content": "What's the weather in SF?"},
    {"type": "function_call", "call_id": "call_123", "name": "get_weather", "arguments": "{\"location\":\"SF\"}"},
    {"type": "function_call_output", "call_id": "call_123", "output": "{\"temp\":70,\"condition\":\"sunny\"}"}
  ],
  "tools": [{"type": "function", "name": "get_weather", "description": "Get weather", "parameters": {"type": "object", "properties": {"location": {"type": "string"}}}}]
}
```

### Response with tool call:

```json
{
  "output": [
    {
      "type": "function_call",
      "id": "fc_456",
      "call_id": "call_456",
      "name": "get_weather",
      "arguments": "{\"location\":\"NYC\"}",
      "status": "completed"
    }
  ]
}
```
