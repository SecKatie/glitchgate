# Feature Specification: Core Proxy with Logging & Cost Monitoring

**Feature Branch**: `001-core-proxy-logging`
**Created**: 2026-03-11
**Status**: Draft
**Input**: User description: "Anthropic and OpenAI compatible endpoints, ability to call disparate LLM service providers starting with Anthropic, proxy LLM usage for logging of all requests/responses, monitoring of cost usage, basic UI for costs and logs."

## Clarifications

### Session 2026-03-11

- Q: Should the proxy support proxy-owned upstream API keys, client credential forwarding, or both? → A: Both modes, configured per provider entry. Multiple provider entries can point to the same upstream service with different auth modes (e.g., a "claude-max" provider with credential forwarding and an "anthropic" provider with a proxy-owned key, both routing to Anthropic's API).
- Q: Should the MVP support multiple proxy API keys with per-key cost attribution? → A: Yes. Multiple keys to segment usage, with per-key cost attribution and filtering. Budget/rate limit enforcement is out of scope for MVP.
- Q: Should the MVP support routing to multiple upstream provider types (Anthropic + OpenAI)? → A: Anthropic-only upstream for MVP. Provider interface designed for extensibility to add more upstream providers later.
- Q: Should the web UI require authentication? → A: Yes. A single "master key" protects the UI for MVP. Future: individual proxy API keys will also be able to log in to view their own logs and usage.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Proxy LLM Requests with Logging (Priority: P1)

A developer points their existing Anthropic SDK at the proxy instead of
directly at the Anthropic API. The proxy forwards requests to the
upstream Anthropic provider and returns responses identically. Every
request and response pair is automatically logged with metadata
(timestamp, model, token counts, latency, status code) for later
inspection. Both standard (synchronous) and streaming (SSE) requests
are supported transparently.

**Why this priority**: This is the foundational capability. Without a
working proxy that logs traffic, no other feature (cost monitoring, UI,
OpenAI compatibility) has anything to build on. A logging proxy alone
is already useful for debugging and auditing LLM usage.

**Independent Test**: Point an Anthropic SDK client at the proxy, send
a chat completion request, verify the response matches what the
provider returns, and confirm a log entry was created with the correct
metadata.

**Acceptance Scenarios**:

1. **Given** the proxy is running with a configured provider and model
   mapping, **When** a user sends an Anthropic Messages API request to
   the proxy, **Then** the proxy resolves the model name to the
   correct provider, forwards the request using the provider's
   configured auth mode, and returns the response unmodified.
2. **Given** the proxy is running, **When** a user sends a streaming
   request, **Then** the proxy forwards SSE chunks incrementally to the
   caller without buffering the full response.
3. **Given** the proxy has processed a request, **When** the response
   completes, **Then** a log entry is persisted containing the request
   body, response body, model name, input/output token counts,
   latency, and timestamp.
4. **Given** the proxy receives a request with an invalid or missing
   proxy API key, **When** the request arrives, **Then** the proxy
   rejects it with an appropriate error before forwarding to the
   upstream provider.
5. **Given** the proxy is configured with a credential-forwarding
   provider (e.g., "claude-max"), **When** a user sends a request
   with their own Authorization header, **Then** the proxy forwards
   that header to the upstream provider and returns the response.

---

### User Story 2 - View Request/Response Logs (Priority: P2)

A developer opens the proxy's built-in web interface and navigates to
the log viewer. They see a chronological list of all proxied requests
with key metadata (timestamp, model, status, token counts, latency).
They can click on any entry to see the full request and response
bodies. They can filter logs by model, status, or date range and
sort by any column.

**Why this priority**: Logging without a way to view the logs has
limited value. A log viewer makes the proxy immediately useful for
debugging, auditing, and understanding LLM usage patterns. It depends
on P1 (logs must exist to be viewed).

**Independent Test**: After proxying several requests, open the web
UI, verify all requests appear in the list, click one to see full
details, and confirm filtering and sorting work correctly.

**Acceptance Scenarios**:

1. **Given** the proxy has logged at least 10 requests, **When** a
   user opens the log viewer in a browser, **Then** they see the most
   recent requests listed with timestamp, model, status, token counts,
   and latency.
2. **Given** the log viewer is open, **When** a user clicks on a log
   entry, **Then** they see the full request and response bodies.
3. **Given** the log viewer is open with mixed model usage, **When** a
   user filters by a specific model, **Then** only requests for that
   model are shown.
4. **Given** the log viewer is open, **When** a user sorts by latency
   descending, **Then** the slowest requests appear first.

---

### User Story 3 - Monitor Cost Usage (Priority: P3)

A developer opens the proxy's built-in web interface and navigates to
the cost dashboard. They see their total spend across all proxied
requests, broken down by model. They can view cost trends over time
(daily/weekly/monthly). This helps them understand and control their
LLM spending.

**Why this priority**: Cost monitoring builds on the logged token
counts from P1 and shares the web UI infrastructure from P2. It
delivers the second core use case (cost awareness) and is independently
valuable once logging and the UI framework exist.

**Independent Test**: After proxying requests across multiple models,
open the cost dashboard, verify total cost matches expected values
based on known model pricing, and confirm per-model and time-based
breakdowns display correctly.

**Acceptance Scenarios**:

1. **Given** the proxy has logged requests across multiple models,
   **When** a user opens the cost dashboard, **Then** they see total
   estimated cost and a breakdown by model.
2. **Given** the cost dashboard is open, **When** a user selects a
   daily view, **Then** they see cost per day for the selected time
   range.
3. **Given** the proxy has processed requests for models with known
   pricing, **When** the cost dashboard calculates totals, **Then** the
   displayed costs are within 5% of the expected values based on token
   counts and published pricing.

---

### User Story 4 - OpenAI-Compatible Endpoint (Priority: P4)

A developer points their existing OpenAI SDK at the proxy. The proxy
accepts OpenAI Chat Completions API format requests, translates them to
the upstream provider's native format (starting with Anthropic),
forwards the request, translates the response back to OpenAI format,
and returns it. The request is logged identically to native Anthropic
requests, with cost tracking included. Both streaming and non-streaming
modes are supported.

**Why this priority**: OpenAI SDK compatibility broadens the proxy's
usefulness to applications already written against the OpenAI API. It
depends on P1 (core proxying) and benefits from P2/P3 (logs and cost
tracking apply automatically). It is a separate story because it
introduces API translation logic that is independent of the core
proxy.

**Independent Test**: Point an OpenAI SDK client at the proxy, send a
chat completion request, verify the response conforms to the OpenAI
response schema, and confirm the request is logged with correct
metadata including token counts and cost.

**Acceptance Scenarios**:

1. **Given** the proxy is running, **When** a user sends an OpenAI Chat
   Completions API request, **Then** the proxy translates it to the
   upstream Anthropic format, forwards it, translates the response
   back to OpenAI format, and returns it.
2. **Given** the proxy receives a streaming OpenAI request, **When**
   the upstream responds, **Then** the proxy translates and forwards
   SSE chunks in OpenAI format incrementally.
3. **Given** the proxy has processed an OpenAI-format request,
   **When** the user views the log, **Then** the entry shows both the
   original (OpenAI) and translated (Anthropic) formats along with
   accurate token counts and cost.

---

### Edge Cases

- What happens when the upstream provider is unreachable or returns a
  5xx error? The proxy MUST return an appropriate error to the caller
  and log the failed attempt with the error details.
- What happens when a streaming response is interrupted mid-stream?
  The proxy MUST close the client connection cleanly and log the
  partial response with an error status.
- What happens when the request body is malformed or does not conform
  to the expected API schema? The proxy MUST reject it with a clear
  validation error before attempting to forward.
- What happens when the proxy's storage is full or unavailable? The
  proxy MUST still forward requests and return responses (logging
  failures MUST NOT block proxying) and MUST emit a warning.
- What happens when a model is used that has no configured pricing?
  The cost dashboard MUST display "unknown" for that model's cost
  rather than omitting or showing zero.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST accept requests conforming to the Anthropic
  Messages API format and forward them to the configured upstream
  Anthropic provider.
- **FR-002**: System MUST accept requests conforming to the OpenAI Chat
  Completions API format, translate them to the upstream provider's
  native format, and return translated responses in OpenAI format.
- **FR-003**: System MUST support streaming (SSE) responses, forwarding
  chunks incrementally to the client without buffering the full
  response.
- **FR-004**: System MUST log every completed request/response pair
  with: timestamp, source API format, model, input token count, output
  token count, latency, status, request body, and response body.
- **FR-005**: System MUST calculate and store the estimated cost of
  each request based on the model used and configured per-token
  pricing.
- **FR-006**: System MUST persist all logs and cost data to durable
  local storage that survives restarts.
- **FR-007**: System MUST provide a web-based log viewer that lists
  logged requests with filtering (by model, status, date range, API
  key) and sorting (by any displayed column).
- **FR-008**: System MUST provide a web-based cost dashboard showing
  total spend, per-model breakdown, per-key breakdown, and cost over
  time (daily/weekly/monthly views). Users MUST be able to filter
  costs by API key.
- **FR-009**: System MUST authenticate incoming requests via proxy
  API key before proxying. The system MUST support multiple proxy
  API keys, each with a human-readable label for identification.
- **FR-010**: System MUST support two upstream authentication modes
  per provider entry: (a) proxy-owned key, where the proxy sends its
  own configured API key to the upstream, and (b) credential
  forwarding, where the proxy forwards the client's Authorization
  header to the upstream. A provider configured for credential
  forwarding MUST NOT require a proxy-owned upstream key.
- **FR-011**: System MUST support configurable model name mapping,
  where a client-facing model name (e.g., "claude-max") maps to a
  specific provider entry and upstream model identifier.
- **FR-012**: System MUST NOT log API keys or secrets in plaintext
  in stored request/response logs. Keys MUST be redacted.
- **FR-013**: System MUST continue proxying requests even if logging
  fails (logging failures MUST NOT block the request path).
- **FR-014**: System MUST be configurable via configuration file,
  environment variables, and CLI flags (in that precedence order,
  with CLI flags highest).
- **FR-015**: System MUST ship model pricing for common Anthropic and
  OpenAI models and allow custom pricing overrides in configuration.
- **FR-016**: System MUST support forwarding specified client headers
  to the upstream provider when configured to do so (required for
  OAuth token passthrough in credential-forwarding mode).
- **FR-017**: System MUST require a master key to access the web UI.
  The UI MUST exchange the master key for a session token on login;
  subsequent requests MUST use the session token, not the master key.
  Requests without a valid session MUST be rejected.

### Key Entities

- **Proxy API Key**: A key issued for authenticating incoming requests,
  with a human-readable label (e.g., "claude-code", "dev-scripts",
  "testing"). Each key is associated with its own usage and cost
  attribution. Keys can be created and revoked.
- **Request Log**: A record of a single proxied request/response pair.
  Attributes: unique ID, timestamp, source API format (anthropic or
  openai), upstream provider, model name, proxy API key used, input
  token count, output token count, latency, HTTP status, request body
  (redacted), response body, estimated cost, error details (if
  applicable).
- **Provider**: A configured upstream LLM service entry with a base
  URL, an authentication mode (proxy-owned key or credential
  forwarding), an optional API key reference, and the list of models
  it serves. Multiple provider entries MAY point to the same upstream
  service with different auth configurations (e.g., "claude-max" for
  credential forwarding and "anthropic" for proxy-owned key, both
  routing to Anthropic's API). The design MUST allow adding new
  provider types without modifying existing provider logic.
- **Model Mapping**: A configuration entry that maps a client-facing
  model name to a specific provider entry and upstream model
  identifier. Enables friendly aliases and provider routing.
- **Model Pricing**: The cost per input token and cost per output token
  for a specific model. Ships with defaults for common models;
  user-configurable overrides are supported.

### Assumptions

- Single-tenant deployment: the proxy is operated by a single user or
  team. Multi-tenant isolation is out of scope for this feature.
- Budget enforcement and rate limiting per API key are out of scope
  for this feature and will be added later.
- Only Anthropic is supported as an upstream provider for MVP. The
  provider interface MUST be designed for extensibility so additional
  upstream providers (e.g., OpenAI) can be added without modifying
  existing code.
- Web UI authentication uses a single master key for MVP. Per-key
  login with scoped views is out of scope for this feature.
- The proxy runs on the same network or machine as the calling
  application. Network latency between caller and proxy is negligible.
- Log storage uses local durable storage. Remote/distributed storage
  is out of scope.
- The web UI is served by the proxy process itself on a configurable
  port. No separate frontend deployment is required.
- Model pricing data is maintained manually in configuration.
  Automatic price fetching from provider APIs is out of scope.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Existing applications work without modification when
  pointed at the proxy instead of directly at the provider (both
  Anthropic and OpenAI SDK clients).
- **SC-002**: Every proxied request is logged and visible in the log
  viewer within 5 seconds of response completion.
- **SC-003**: Cost estimates displayed in the dashboard are within 5%
  of actual provider invoices for the same usage period.
- **SC-004**: Users experience no perceptible additional delay compared
  to connecting directly to the provider (proxy overhead under 50ms
  for non-streaming requests).
- **SC-005**: The log viewer and cost dashboard load and display data
  within 2 seconds under normal usage (up to 100,000 stored log
  entries).
- **SC-006**: The proxy runs reliably on a single modest machine
  (2 vCPU / 2 GB RAM) handling at least 50 concurrent proxied
  requests without degradation.
