# Feature Specification: OpenAI Responses API Support

**Feature Branch**: `010-responses-api-support`
**Created**: 2026-03-12
**Status**: Draft
**Supersedes**: Spec 009 (OpenAI Upstream Provider) — never implemented
**Input**: User description: "Build OpenAI Responses API to Responses API proxying. The other input providers (Anthropic and OpenAI Chat Completions) should be translatable to Responses API and Responses API should work with any other upstream. If features of the Responses API are not supported by the upstream then we drop them or error if there is no way to translate."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Proxy Responses API Requests to a Responses API Upstream (Priority: P1)

An operator configures a provider that speaks the Responses API natively (e.g., OpenAI). A client sends a request in Responses API format to the proxy's `/v1/responses` endpoint. The proxy authenticates the client, forwards the request to the upstream Responses API provider, and returns the response to the client in Responses API format — a clean passthrough with logging, cost tracking, and fallback support.

**Why this priority**: This is the simplest and most direct integration. No translation is needed, making it the foundation that proves the Responses API input path works end-to-end before adding translation complexity.

**Independent Test**: Can be fully tested by configuring a Responses API upstream, sending a Responses API request to the proxy, and verifying the response is returned correctly with token usage logged.

**Acceptance Scenarios**:

1. **Given** a provider is configured with Responses API as its upstream format, **When** a client sends a non-streaming Responses API request to `/v1/responses`, **Then** the proxy forwards the request, returns the upstream response in Responses API format, and logs token usage.
2. **Given** a provider is configured with Responses API as its upstream format, **When** a client sends a streaming Responses API request, **Then** the proxy streams events back to the client in Responses API SSE format without buffering the full response.
3. **Given** a Responses API request includes tool calls, **When** the upstream returns tool call results, **Then** tool calls and results are passed through correctly.
4. **Given** a Responses API request includes image or audio content, **When** the upstream supports the modality, **Then** the multimodal content is passed through correctly.
5. **Given** the upstream returns an error, **When** the proxy receives the error, **Then** it returns an appropriate error response to the client in Responses API error format.

---

### User Story 2 - Proxy Responses API Requests to Non-Responses-API Upstreams (Priority: P2)

A client sends a request in Responses API format, but the resolved upstream provider speaks a different format (Anthropic Messages API or OpenAI Chat Completions). The proxy translates the incoming Responses API request into the upstream's native format, forwards it, and translates the upstream response back into Responses API format before returning it to the client.

**Why this priority**: This enables Responses API clients to access any upstream provider the proxy supports, which is the core value proposition — a single client format that works everywhere.

**Independent Test**: Can be tested by configuring an Anthropic or Chat Completions upstream, sending a Responses API request, and verifying the response is returned correctly in Responses API format.

**Acceptance Scenarios**:

1. **Given** a Responses API request is routed to an Anthropic upstream, **When** the proxy translates and forwards the request, **Then** the response is translated back to Responses API format with correct content, tool calls, and token usage.
2. **Given** a Responses API request is routed to an OpenAI Chat Completions upstream, **When** the proxy translates and forwards the request, **Then** the response is translated back to Responses API format correctly.
3. **Given** a Responses API request contains features not supported by the upstream (e.g., Responses API-specific parameters), **When** translation occurs, **Then** unsupported features are silently dropped if the request can still be fulfilled, or the proxy returns an error if the feature is essential to fulfilling the request.
4. **Given** a streaming Responses API request is routed to a non-Responses-API upstream, **When** the upstream streams its response, **Then** the proxy translates the stream into Responses API SSE events and streams them to the client.
5. **Given** a Responses API request includes image content routed to an Anthropic upstream, **When** translation occurs, **Then** the image content is translated into the Anthropic image content block format and the response is translated back correctly.
6. **Given** a Responses API request includes a modality not supported by the upstream (e.g., audio to a text-only provider), **When** translation occurs, **Then** the proxy returns a clear error indicating the modality is unsupported by the resolved provider.

---

### User Story 3 - Translate Anthropic and Chat Completions Input to Responses API Upstream (Priority: P3)

A client sends a request in Anthropic Messages format or OpenAI Chat Completions format, but the resolved upstream provider speaks the Responses API. The proxy translates the incoming request into Responses API format, forwards it, and translates the upstream Responses API response back into the client's original format.

**Why this priority**: This completes the translation matrix, ensuring every input format can reach every upstream format. It allows operators to migrate upstreams to Responses API providers without requiring client changes.

**Independent Test**: Can be tested by configuring a Responses API upstream, sending an Anthropic-format or Chat Completions-format request, and verifying the response is returned in the client's original format.

**Acceptance Scenarios**:

1. **Given** an Anthropic-format request is routed to a Responses API upstream, **When** the proxy translates and forwards the request, **Then** the response is translated back to Anthropic Messages format correctly.
2. **Given** an OpenAI Chat Completions-format request is routed to a Responses API upstream, **When** the proxy translates and forwards the request, **Then** the response is translated back to Chat Completions format correctly.
3. **Given** a streaming Anthropic request is routed to a Responses API upstream, **When** the upstream streams its response, **Then** the proxy translates the Responses API stream into Anthropic SSE events.
4. **Given** a streaming Chat Completions request is routed to a Responses API upstream, **When** the upstream streams its response, **Then** the proxy translates the Responses API stream into Chat Completions SSE events.

---

### User Story 4 - OpenAI Chat Completions Upstream Provider (Priority: P2)

An operator configures an OpenAI-compatible endpoint as an upstream provider targeting Chat Completions. Clients send requests in any supported format (Anthropic, Chat Completions, or Responses API) and the proxy routes them to the Chat Completions endpoint, translating as needed. This covers any OpenAI-compatible endpoint (api.openai.com, Azure OpenAI, LiteLLM, local models, etc.).

**Why this priority**: Chat Completions is the most widely deployed OpenAI-compatible API format. Supporting it as an upstream unlocks a large ecosystem of providers for all proxy clients. Equal priority with User Story 2 since both enable cross-format routing.

**Independent Test**: Can be tested by configuring an OpenAI Chat Completions upstream, sending requests in each input format, and verifying correct responses with token usage logged.

**Acceptance Scenarios**:

1. **Given** OpenAI is configured as a Chat Completions provider with a valid API key, **When** a client sends an Anthropic-format request routed to the OpenAI model, **Then** the proxy translates the request, forwards it to Chat Completions, and returns a correctly formatted Anthropic response.
2. **Given** OpenAI is configured as a Chat Completions provider, **When** a client sends a Chat Completions-format request, **Then** the proxy forwards the request directly without double-translation.
3. **Given** OpenAI is configured with `forward` auth mode, **When** a client provides their own API key, **Then** the proxy uses the client's key to authenticate with the upstream.
4. **Given** OpenAI is configured with `proxy_key` auth mode, **When** a client sends a request, **Then** the proxy authenticates with the upstream using the proxy's configured API key.
5. **Given** a valid response is returned from the upstream, **When** the request completes, **Then** token usage (input and output tokens) is captured and stored in the request log.

---

### User Story 5 - OpenAI Providers in Fallback Chains (Priority: P3)

An operator uses OpenAI providers (Chat Completions or Responses API) as entries within a fallback chain alongside other providers. If an earlier chain entry fails with a retryable error, the proxy falls back to the OpenAI provider transparently, translating formats as needed.

**Why this priority**: Fallback chains are a core proxy feature and OpenAI providers should participate in them just like Anthropic providers do. Equal priority with User Story 3 as both complete the integration story.

**Independent Test**: Can be tested by configuring a fallback chain with a failing provider followed by an OpenAI provider, triggering a retryable failure, and verifying the request succeeds via the OpenAI fallback.

**Acceptance Scenarios**:

1. **Given** a model chain where an earlier entry returns a 5xx error, **When** the proxy retries, **Then** it successfully routes to the OpenAI provider entry and returns a valid response.
2. **Given** a model chain where an earlier entry returns a 429 rate-limit response, **When** the proxy retries, **Then** it successfully routes to the OpenAI provider entry.

---

### Edge Cases

- What happens when a Responses API request includes `previous_response_id` (conversation state)? The proxy does not manage conversation state; this parameter is passed through to Responses API upstreams. For non-Responses-API upstreams, `previous_response_id` is silently dropped and the proxy proceeds using the `input` field as the conversation context (since `input` carries the full conversation).
- What happens when a Responses API request uses modalities not supported by the upstream (e.g., audio input to a text-only upstream)? The proxy returns a clear error indicating the modality is unsupported by the resolved provider rather than silently dropping the content.
- What happens when a Responses API request includes built-in tools (web search, file search, code interpreter)? These are passed through to Responses API upstreams and return an error for non-Responses-API upstreams since they cannot be translated.
- What happens when token usage fields are absent from the upstream response? The request is logged with zero tokens rather than failing.
- What happens when a Responses API client sends a request and the resolved model has a fallback chain? The proxy attempts each provider in the chain, translating to each provider's native format as needed.
- What happens when the Responses API response includes output types not representable in the client's format (e.g., Responses API-specific output items to an Anthropic client)? The proxy maps what it can and drops unrepresentable content with a warning in the response metadata if possible, or errors if the content is essential.
- What happens when the OpenAI API key is invalid or expired? The proxy returns an authentication error to the client.
- What happens when streaming is requested but the upstream returns a non-streaming response? The proxy returns an error or adapts gracefully.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST support an OpenAI provider type that can target any upstream endpoint speaking OpenAI API formats (including but not limited to api.openai.com, Azure OpenAI, LiteLLM, and locally hosted OpenAI-compatible models), configured via a base URL.
- **FR-002**: Operators MUST be able to configure an OpenAI provider to target either Chat Completions or Responses API as the upstream endpoint, declared explicitly in provider configuration.
- **FR-003**: The system MUST support two authentication modes for OpenAI providers: proxy-managed (proxy uses its own configured API key) and client-forwarded (proxy forwards the client's provided API key).
- **FR-004**: The system MUST expose a `/v1/responses` endpoint that accepts requests in OpenAI Responses API format.
- **FR-005**: The system MUST support Responses API as both an input format (client-facing) and an output format (upstream-facing), bringing the total supported formats to three: Anthropic Messages, OpenAI Chat Completions, and OpenAI Responses API.
- **FR-006**: The system MUST translate Responses API input requests to Anthropic Messages format when the resolved upstream provider speaks Anthropic.
- **FR-007**: The system MUST translate Responses API input requests to OpenAI Chat Completions format when the resolved upstream provider speaks Chat Completions.
- **FR-008**: The system MUST pass through Responses API input requests directly when the resolved upstream provider speaks Responses API (no translation).
- **FR-009**: The system MUST translate Anthropic Messages input requests to Responses API format when the resolved upstream provider speaks Responses API.
- **FR-010**: The system MUST translate OpenAI Chat Completions input requests to Responses API format when the resolved upstream provider speaks Responses API.
- **FR-011**: The system MUST accept requests from clients in Anthropic Messages format and translate them to the appropriate OpenAI upstream format (Chat Completions or Responses API) when the resolved provider is an OpenAI provider.
- **FR-012**: The system MUST accept requests from clients in OpenAI Chat Completions format and route them to an OpenAI Chat Completions upstream without double-translation.
- **FR-013**: The system MUST translate upstream Responses API responses back into the client's input format (Anthropic, Chat Completions, or Responses API).
- **FR-014**: The system MUST support streaming for all format combinations, including translating between Responses API SSE events, Chat Completions SSE events, and Anthropic SSE events.
- **FR-015**: The system MUST extract token usage (input tokens, output tokens) from all OpenAI provider responses (Chat Completions and Responses API) and record them in the request log.
- **FR-016**: The system MUST log requests arriving via the `/v1/responses` endpoint with source format `"responses"` to distinguish them from Chat Completions (`"openai"`) and Anthropic (`"anthropic"`) traffic in logs and the web UI.
- **FR-017**: The system MUST handle unsupported Responses API features gracefully: content-bearing features (messages, tool calls, modalities) are essential and MUST produce a clear error if they cannot be translated; behavioral parameters (temperature, top_p, reasoning effort, truncation strategy, etc.) are optional and MUST be silently dropped or mapped to the closest equivalent in the target format.
- **FR-018**: The system MUST support tool use translation between all format combinations, mapping function calls and results correctly.
- **FR-019**: OpenAI providers MUST participate in model fallback chains alongside other provider types, retried on the same retryable error conditions (server errors, rate limits).
- **FR-020**: The system MUST include OpenAI-specific cost calculation in the pricing module so that logged costs reflect per-model pricing for OpenAI providers.
- **FR-021**: The system MUST authenticate Responses API clients using the same proxy key mechanism used for other input formats.
- **FR-022**: The system MUST support multimodal content (images, audio, files/documents) in Responses API requests and translate them to equivalent content types in other formats where the upstream supports them.
- **FR-023**: The system MUST translate image content between Responses API format and Anthropic image content blocks when routing between these formats.
- **FR-024**: The system MUST return a clear error when a request includes a modality (e.g., audio, image) that the resolved upstream provider does not support, rather than silently dropping the content.

### Key Entities

- **OpenAI Provider Configuration**: Represents an upstream OpenAI-format endpoint with attributes including the target API type (Chat Completions or Responses API), base URL (any OpenAI-compatible endpoint), authentication mode, and credentials.
- **Responses API Request**: The request structure defined by OpenAI's Responses API, including model, input messages (text, images, audio, files), tools, instructions, and configuration parameters. Structurally distinct from Chat Completions.
- **Responses API Response**: The response structure returned by the Responses API, including output content items, usage statistics, and metadata. Uses a different schema than Chat Completions responses.
- **Responses API SSE Events**: The streaming event format used by the Responses API, which differs from Chat Completions streaming in event types and payload structure.
- **Format Translation Matrix**: The mapping of all supported input formats to all supported output formats. With three formats (Anthropic, Chat Completions, Responses API), this creates a 3x3 matrix where the diagonal represents passthrough and off-diagonal cells represent translations.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can configure an OpenAI provider (Chat Completions or Responses API) as an upstream and successfully proxy requests end-to-end without manual format conversion.
- **SC-002**: A client using Responses API format can successfully send requests through the proxy and receive correct responses from any configured upstream provider type (Anthropic, Chat Completions, or Responses API).
- **SC-003**: A client using Anthropic or Chat Completions format can successfully send requests through the proxy to a Responses API upstream and receive correct responses in their original format.
- **SC-004**: Clients sending OpenAI Chat Completions-format requests to an OpenAI Chat Completions upstream receive correct responses without any format degradation or data loss from unnecessary conversion.
- **SC-005**: Token usage from all OpenAI provider responses (Chat Completions and Responses API) is recorded in the request log for 100% of completed requests.
- **SC-006**: Streaming works correctly in all format combinations involving the Responses API and Chat Completions, with events delivered incrementally without full-response buffering.
- **SC-007**: Unsupported Responses API features produce clear, actionable error messages rather than silent failures or corrupted responses when those features are essential.
- **SC-008**: All existing proxy features — request logging, cost calculation, fallback chains, authentication, streaming — continue to work correctly when OpenAI providers are involved as either input or output format.
- **SC-009**: Tool use round-trips (function calling and result submission) work correctly across all format combinations.
- **SC-010**: Multimodal content (images, audio, files) is correctly translated between Responses API and other formats when the upstream supports the modality, and produces clear errors when it does not.

## Clarifications

### Session 2026-03-12

- Q: Which version of the Responses API should the proxy target? → A: Track the latest stable Responses API schema; evolve with OpenAI updates rather than pinning to a specific snapshot.
- Q: How should "essential" vs "optional" features be classified for FR-011? → A: Content-bearing features (messages, tool calls, modalities) are essential — error if untranslatable. Behavioral parameters (temperature, top_p, reasoning effort, truncation strategy, etc.) are optional — silently drop or map to closest equivalent.
- Q: What is the relationship between spec 010 and spec 009 (OpenAI Upstream Provider)? → A: Spec 010 fully supersedes spec 009. 009 was never implemented. 010 covers the complete end-to-end OpenAI support: the upstream provider implementation, Responses API as both input and output format, Chat Completions upstream support, and the full translation matrix.
- Q: Should `previous_response_id` on non-Responses upstreams be a hard error or warning? → A: Warning — silently drop `previous_response_id` and proceed using the `input` field as the conversation context, since `input` carries the full conversation.
- Q: Should Responses API requests be logged with a distinct source format? → A: Yes, log as `"responses"` to distinguish from Chat Completions traffic in logs and the web UI.

## Assumptions

- The proxy targets the latest stable OpenAI Responses API schema at time of implementation and evolves with upstream changes, consistent with how the proxy handles Anthropic API evolution. No version pinning or multi-version support.
- The proxy treats each Responses API request as stateless. Server-side conversation state features (like `previous_response_id`) are passed through to Responses API upstreams and silently dropped for other upstreams (the `input` field carries the full conversation context).
- Built-in Responses API tools (web search, file search, code interpreter) are passed through to Responses API upstreams only and are not emulated for other upstream types.
- Multimodal content (images, audio, files/documents) is in scope. The proxy translates multimodal content between formats where possible and returns clear errors when a modality is unsupported by the resolved upstream. The proxy does not transcode media (e.g., converting audio formats); it passes through or maps content references as-is.
- Responses API streaming uses server-sent events compatible with the proxy's existing streaming infrastructure.
- The `APIFormat()` method on the provider interface will be extended to support a third value (`"responses"`) alongside the existing `"anthropic"` and `"openai"` values.
- Per-model pricing for Responses API interactions is operator-configured, consistent with existing pricing configuration patterns.
