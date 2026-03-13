# Feature Specification: OpenAI Upstream Provider Support

**Feature Branch**: `009-openai-upstream-provider`
**Created**: 2026-03-12
**Status**: Draft

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Route Requests to OpenAI Chat Completions (Priority: P1)

An operator configures OpenAI as an upstream provider in the proxy configuration. Clients send requests in either Anthropic Messages format or OpenAI Chat Completions format and the proxy routes them to OpenAI's Chat Completions endpoint, handling authentication, logging, and cost tracking the same way it does for other providers.

**Why this priority**: Chat Completions is OpenAI's primary and most widely supported API. This unlocks OpenAI models for all existing proxy clients immediately and is foundational to the Responses API work.

**Independent Test**: Can be fully tested by configuring OpenAI Chat Completions as an upstream, sending a request through the proxy, and verifying the response is returned correctly with token usage logged.

**Acceptance Scenarios**:

1. **Given** OpenAI is configured as a provider with a valid API key, **When** a client sends an Anthropic-format request routed to an OpenAI model, **Then** the proxy translates the request, forwards it to OpenAI Chat Completions, and returns a correctly formatted response.
2. **Given** OpenAI is configured as a provider, **When** a client sends an OpenAI-format request routed to an OpenAI model, **Then** the proxy forwards the request directly to OpenAI Chat Completions without double-translation.
3. **Given** OpenAI is configured with `forward` auth mode, **When** a client provides their own API key, **Then** the proxy uses the client's key to authenticate with OpenAI rather than the proxy's own key.
4. **Given** OpenAI is configured with `proxy_key` auth mode, **When** a client sends a request, **Then** the proxy authenticates with OpenAI using the proxy's configured API key regardless of client credentials.
5. **Given** a valid response is returned from OpenAI, **When** the request completes, **Then** token usage (input and output tokens) is captured and stored in the request log.

---

### User Story 2 - Route Requests to OpenAI Responses API (Priority: P2)

An operator configures a provider endpoint pointing to OpenAI's Responses API. When a request is routed to that provider, the proxy translates the incoming request format (Anthropic Messages or OpenAI Chat Completions) to the Responses API format, forwards it, and translates the response back.

**Why this priority**: The Responses API is OpenAI's newer, richer API surface with capabilities not available in Chat Completions. Supporting it as an upstream allows operators to use advanced OpenAI features while clients continue using their existing format.

**Independent Test**: Can be fully tested by configuring a provider targeting the Responses API, sending a request, and verifying the response is correctly translated and returned with full token logging.

**Acceptance Scenarios**:

1. **Given** a provider is configured to use the Responses API endpoint, **When** a client sends an Anthropic-format request, **Then** the proxy translates the request to Responses API format, forwards it, and returns an Anthropic-format response.
2. **Given** a provider is configured to use the Responses API endpoint, **When** a client sends an OpenAI Chat Completions-format request, **Then** the proxy translates to Responses API format, forwards it, and returns an OpenAI Chat Completions-format response.
3. **Given** a Responses API request includes tool use, **When** the response is returned, **Then** tool calls are correctly translated back into the client's expected format.
4. **Given** streaming is requested, **When** the Responses API returns a streaming response, **Then** the proxy streams events back to the client in the client's expected format without buffering the full response.
5. **Given** a valid streaming or non-streaming response, **When** the request completes, **Then** token usage is captured from the Responses API response and stored in the request log.

---

### User Story 3 - OpenAI Provider in Fallback Chains (Priority: P3)

An operator uses OpenAI providers (Chat Completions or Responses API) as entries within a fallback chain alongside other providers. If an earlier chain entry fails with a retryable error, the proxy falls back to the OpenAI provider transparently.

**Why this priority**: Fallback chains are a core proxy feature and OpenAI providers should participate in them just like Anthropic providers do.

**Independent Test**: Can be tested by configuring a fallback chain with a failing provider followed by an OpenAI provider, triggering a retryable failure, and verifying the request succeeds via the OpenAI fallback.

**Acceptance Scenarios**:

1. **Given** a model chain where an earlier entry returns a 5xx error, **When** the proxy retries, **Then** it successfully routes to the OpenAI provider entry and returns a valid response.
2. **Given** a model chain where an earlier entry returns a 429 rate-limit response, **When** the proxy retries, **Then** it successfully routes to the OpenAI provider entry.

---

### Edge Cases

- What happens when the OpenAI API key is invalid or expired? The proxy returns an authentication error to the client.
- What happens when the Responses API returns a response format the proxy does not recognize? The proxy returns an error rather than passing through malformed data.
- What happens when streaming is requested but the upstream returns a non-streaming response? The proxy returns an error or adapts gracefully.
- What happens when token counts are absent from the upstream response? The request is logged with zero tokens rather than failing.
- What happens when a request contains features supported by the Responses API but not Chat Completions (or vice versa)? The proxy handles supported fields and ignores or errors on unsupported fields with a clear error message.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST support an OpenAI-format provider type that works with any upstream endpoint speaking the OpenAI API format (including but not limited to `api.openai.com`, Azure OpenAI, LiteLLM, and locally hosted OpenAI-compatible models), configured via a base URL.
- **FR-002**: Operators MUST be able to configure a provider to target OpenAI's Chat Completions endpoint or Responses API endpoint independently.
- **FR-003**: The system MUST support two authentication modes for OpenAI providers: proxy-managed (proxy uses its own configured API key) and client-forwarded (proxy forwards the client's provided API key).
- **FR-004**: The system MUST accept requests from clients in Anthropic Messages format and translate them to the appropriate OpenAI upstream format (Chat Completions or Responses API) when the resolved provider is an OpenAI provider.
- **FR-005**: The system MUST accept requests from clients in OpenAI Chat Completions format and route them to an OpenAI Chat Completions upstream without double-translation.
- **FR-006**: The system MUST accept requests from clients in OpenAI Chat Completions format and translate them to Responses API format when the resolved provider targets the Responses API.
- **FR-007**: The system MUST support streaming responses from both OpenAI Chat Completions and Responses API endpoints and forward streamed events to clients in the client's expected format.
- **FR-008**: The system MUST extract token usage (input tokens, output tokens) from OpenAI responses and record them in the request log.
- **FR-009**: OpenAI providers MUST participate in model fallback chains and be retried on the same retryable error conditions as other provider types (server errors, rate limits).
- **FR-010**: The system MUST translate tool use and tool call results between client formats and OpenAI upstream formats when routing through an OpenAI provider.
- **FR-011**: The system MUST include OpenAI-specific cost calculation in the pricing module so that logged costs reflect the configured per-model pricing for OpenAI providers.

### Key Entities

- **OpenAI Provider Configuration**: Represents an upstream OpenAI-format endpoint with attributes including the target API type (Chat Completions or Responses API), base URL (any OpenAI-compatible endpoint), authentication mode, and credentials.
- **Responses API Request**: The request structure expected by OpenAI's Responses API, distinct from Chat Completions in schema and field names.
- **Responses API Response**: The response structure returned by OpenAI's Responses API, including usage statistics and content in Responses API format.
- **Token Usage Record**: Provider-agnostic count of input and output tokens extracted from an upstream response and stored in the request log.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can configure OpenAI Chat Completions as an upstream and successfully proxy a request end-to-end without manual format conversion.
- **SC-002**: An operator can configure the Responses API as an upstream and successfully proxy both streaming and non-streaming requests end-to-end.
- **SC-003**: Token usage from all OpenAI provider responses (Chat Completions and Responses API) is recorded in the request log for 100% of completed requests.
- **SC-004**: OpenAI providers integrate with fallback chains so that a chain containing an OpenAI entry retries correctly on retryable upstream failures.
- **SC-005**: Clients sending OpenAI Chat Completions-format requests to an OpenAI Chat Completions upstream receive correct responses without any format degradation or data loss from unnecessary conversion.
- **SC-006**: All existing proxy features—request logging, cost calculation, fallback chains, streaming—continue to work correctly when OpenAI providers are involved.

## Clarifications

### Session 2026-03-12

- Q: Should the OpenAI provider type target only `api.openai.com` or any OpenAI-format endpoint (Azure, LiteLLM, Ollama, etc.)? → A: Any OpenAI-format compatible endpoint (any configurable base URL that speaks the OpenAI API format).

## Assumptions

- The OpenAI Responses API is treated as a distinct upstream endpoint type from Chat Completions; configuration explicitly selects one or the other rather than the proxy auto-detecting.
- The proxy does not expose a new `/v1/responses` client-facing entry point; clients continue to use the existing Anthropic or OpenAI Chat Completions entry points.
- OpenAI streaming uses server-sent events (SSE) in a format compatible with the existing streaming infrastructure.
- Responses API stateful features (conversation state, session IDs) are not proxied or managed by the proxy; each request is treated as stateless.
- Per-model pricing for OpenAI models is operator-configured in the proxy config, consistent with how Anthropic pricing is configured.
- The proxy does not support OpenAI's file, image, or audio modalities in this feature; only text and tool use are in scope.
