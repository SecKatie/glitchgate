# Research: OpenAI Responses API Support

**Feature**: 010-responses-api-support
**Date**: 2026-03-12

## Decision 1: OpenAI Provider Architecture

**Decision**: Create a single `internal/provider/openai/` package with a configurable `api_type` field that selects between Chat Completions and Responses API upstream formats. The provider returns `APIFormat() == "openai"` for Chat Completions and `APIFormat() == "responses"` for Responses API.

**Rationale**: The existing provider interface uses `APIFormat()` to drive handler routing. Using two distinct format values keeps the routing logic clean and allows handlers to branch on format without inspecting provider internals. A single package avoids duplicating auth, base URL, and HTTP client logic.

**Alternatives considered**:
- Two separate provider packages (`provider/openai_chat/`, `provider/openai_responses/`): Rejected — duplicates auth/config code, violates DRY
- Single "openai" format value with sub-routing inside handlers: Rejected — forces handlers to inspect provider config details, breaks clean abstraction

## Decision 2: Configuration Schema Extension

**Decision**: Extend `ProviderConfig` with:
- `Type: "openai"` (new provider type alongside "anthropic" and "github_copilot")
- `APIType: "chat_completions" | "responses"` (selects upstream endpoint format)

**Rationale**: Explicit configuration follows the existing pattern where operators declare provider capabilities. The proxy uses this to select the correct translation path at startup rather than runtime detection.

**Alternatives considered**:
- Auto-detect based on base URL: Rejected — unreliable for custom endpoints (LiteLLM, local models)
- Merge into existing "github_copilot" type: Rejected — Copilot has unique OAuth flow; clean separation is better

## Decision 3: Responses API Type Definitions

**Decision**: Define Responses API types in `internal/translate/responses_types.go`, following the same pattern as `openai_types.go`. Key types:

- `ResponsesRequest`: Top-level request with `Model`, `Input` (json.RawMessage for string|[]InputItem), `Instructions`, `Tools`, and behavioral params
- `ResponsesResponse`: Response with `Output` ([]OutputItem), `Usage`, `Status`, `Error`
- `OutputItem`: Union type discriminated by `Type` field (message, function_call, function_call_output, etc.)
- `InputItem`: Union type for input content (input_text, input_image, input_file, function_call, function_call_output, etc.)
- `ResponsesUsage`: Token counts with `InputTokens`, `OutputTokens`, `TotalTokens` plus detail breakdowns
- `ResponsesStreamEvent`: SSE event envelope with `Type` discriminator

**Rationale**: Following existing conventions keeps the codebase consistent. Using `json.RawMessage` for polymorphic fields (Input, Output) allows lazy parsing and avoids interface{} where possible.

**Alternatives considered**:
- Using generics for union types: Rejected — Go generics don't solve JSON union deserialization cleanly
- Single mega-struct with all optional fields: Rejected — loses type safety and documentation value

## Decision 4: Translation Matrix Design

**Decision**: Implement 6 new translation paths as pure functions in `internal/translate/`:

| From → To | Request Function | Response Function |
|-----------|-----------------|-------------------|
| Responses → Anthropic | `ResponsesToAnthropic()` | `AnthropicToResponsesResponse()` |
| Responses → Chat Completions | `ResponsesToOpenAI()` | `OpenAIToResponsesResponse()` |
| Anthropic → Responses | `AnthropicToResponses()` | `ResponsesToAnthropicResponse()` |
| Chat Completions → Responses | `OpenAIToResponses()` | `ResponsesToOpenAIResponse()` |

Plus: `RelayResponsesSSEStream()` for passthrough and `ResponsesSSEToAnthropicSSE()` / `ResponsesSSEToOpenAISSE()` / `AnthropicSSEToResponsesSSE()` / `OpenAISSEToResponsesSSE()` for streaming translation.

**Rationale**: Pure functions match existing patterns (e.g., `OpenAIToAnthropic()`). Each function handles one direction of one format pair, making them independently testable. Streaming translators handle the SSE event format differences.

**Alternatives considered**:
- Route everything through an intermediate "canonical" format: Rejected — adds unnecessary translation hops and latency; constitution requires minimal overhead
- Use a single generic translator with format parameters: Rejected — loses type safety and makes testing harder

## Decision 5: Responses API Streaming Architecture

**Decision**: The Responses API uses typed semantic SSE events (53 event types) vs Chat Completions' flat delta model. For streaming translation:

1. **Responses → Client (passthrough)**: `RelayResponsesSSEStream()` forwards events unchanged, extracts tokens from `response.completed` event
2. **Responses → Anthropic SSE**: Map `response.output_text.delta` → `content_block_delta`, `response.completed` → `message_delta` + `message_stop`, etc.
3. **Responses → OpenAI SSE**: Map `response.output_text.delta` → `choices[0].delta.content`, `response.function_call_arguments.delta` → `choices[0].delta.tool_calls[0].function.arguments`, etc.
4. **Anthropic SSE → Responses**: Map `content_block_delta` → `response.output_text.delta`, `message_stop` → `response.completed`, etc.
5. **OpenAI SSE → Responses**: Map `choices[0].delta` → typed Responses events

**Rationale**: Responses API streaming is fundamentally different — it's event-typed rather than delta-based. Translation requires semantic mapping, not just reformatting. Token extraction from `response.completed` event follows the same pattern as extracting from Anthropic's `message_delta`.

**Alternatives considered**:
- Buffer streaming responses and return non-streaming: Rejected — violates constitution (Principle IV: "never buffered to completion")
- Only support non-streaming for cross-format: Rejected — streaming is a core proxy feature

## Decision 6: Multimodal Content Translation

**Decision**: Translate multimodal content between formats where semantically equivalent:

| Responses API | Anthropic | Chat Completions |
|--------------|-----------|-----------------|
| `input_text` | text content block | `{type: "text", text}` |
| `input_image` (URL) | `{type: "image", source: {type: "url"}}` | `{type: "image_url", image_url: {url}}` |
| `input_image` (base64) | `{type: "image", source: {type: "base64"}}` | `{type: "image_url", image_url: {url: "data:..."}}` |
| `input_file` | Error (no equivalent) | Error (no equivalent) |
| `input_audio` | Error (no Anthropic equivalent) | `{type: "input_audio"}` (if CC supports) |

**Rationale**: The proxy translates what it can and errors clearly on what it can't (per FR-017/FR-024). File input is Responses API-specific with no equivalent in other formats. Audio support varies by provider.

**Alternatives considered**:
- Silently drop unsupported modalities: Rejected — spec requires clear errors for content-bearing features (essential classification from clarifications)
- Transcode between formats: Rejected — spec explicitly states no transcoding; pass through or map references as-is

## Decision 7: Tool Definition Translation

**Decision**: Map between the three tool definition formats:

| Responses API (flat) | Chat Completions (nested) | Anthropic |
|---------------------|--------------------------|-----------|
| `{type, name, description, parameters}` | `{type, function: {name, description, parameters}}` | `{name, description, input_schema}` |

Tool calls: `function_call` output item ↔ `tool_calls[]` ↔ `tool_use` content block
Tool results: `function_call_output` input item ↔ `tool` role message ↔ `tool_result` content block

`tool_choice` mapping:
- `"auto"` / `"none"` / `"required"`: Same across formats (with Anthropic using `{"type": "any"}` for "required")
- `{"type": "function", "name": X}`: Responses flat → CC nested → Anthropic `{"type": "tool", "name": X}`
- Responses-only choices (`allowed_tools`, built-in tool forcing): Error for non-Responses upstreams

**Rationale**: Extends existing tool translation patterns (OpenAI↔Anthropic) to include Responses API. The flat vs nested distinction is the primary structural difference.

## Decision 8: Error Handling Strategy

**Decision**: Error translation follows the essential/optional classification:

- **Essential features** (content, tools, modalities): Return 400 with clear error message indicating what's unsupported and why
- **Optional features** (temperature, reasoning, truncation, store, background, etc.): Silently drop or map to closest equivalent
- **Upstream errors**: Map error codes semantically between formats (extend existing `AnthropicErrorToOpenAI` pattern)
- **Responses-only features on non-Responses upstreams**: `previous_response_id` silently dropped; `built-in tools` return error; `input_file` returns error

**Rationale**: Aligns with clarification session decision. Operators shouldn't need to strip optional parameters from client requests; the proxy handles format differences transparently.
