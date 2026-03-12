# Feature Specification: GitHub Copilot Provider

**Feature Branch**: `006-github-copilot-provider`
**Created**: 2026-03-11
**Status**: Draft
**Input**: User description: "Build support for proxying GitHub Copilot like how litellm does it, with extra headers built into the provider itself."

## Clarifications

### Session 2026-03-11

- Q: Should OAuth device flow trigger at startup (blocking), lazily on first request, or via a separate CLI command? → A: Standalone CLI subcommand `llm-proxy auth copilot` that runs independently of the proxy server — can be run before or after the provider is configured.
- Q: How should stored OAuth token files be protected at rest? → A: Restricted file permissions — store as plaintext JSON with 0600 (owner-only read/write).
- Q: Should Copilot models accept requests on both API paths or only OpenAI format? → A: Translate both directions — accept requests on both `/v1/messages` (Anthropic) and `/v1/chat/completions` (OpenAI) for Copilot models, translating as needed.
- Q: Where should OAuth tokens be stored by default? → A: `~/.config/llm-proxy/copilot/` (XDG-conventional, co-located with proxy config).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Proxy requests through GitHub Copilot (Priority: P1)

An operator configures llm-proxy with a GitHub Copilot provider and maps model names (e.g., `gc/claude-opus-4.6`) to upstream Copilot models. Clients send requests using the standard OpenAI chat completions format to llm-proxy, and the proxy forwards them to the GitHub Copilot API with the correct authentication and required headers.

**Why this priority**: This is the core value proposition — enabling users to route LLM requests through their GitHub Copilot subscription, giving access to multiple model families (Claude, GPT, Gemini, etc.) through a single proxy.

**Independent Test**: Can be fully tested by configuring a Copilot provider in the config file, sending an OpenAI-format chat completion request through the proxy, and verifying a successful response is returned from the Copilot API.

**Acceptance Scenarios**:

1. **Given** a configured GitHub Copilot provider with valid credentials, **When** a client sends a chat completion request for model `gc/claude-sonnet-4.6`, **Then** the proxy forwards the request to the Copilot API chat completions endpoint with the correct headers and returns the upstream response.
2. **Given** a configured GitHub Copilot provider, **When** a streaming chat completion request is sent, **Then** the proxy streams SSE events back to the client in real time.
3. **Given** an expired or invalid Copilot token, **When** a request is sent, **Then** the proxy returns an appropriate error message indicating authentication failure.
4. **Given** a configured GitHub Copilot provider, **When** a client sends an Anthropic-format request to `/v1/messages` for a Copilot model, **Then** the proxy translates the request to OpenAI format, forwards it to the Copilot API, and translates the response back to Anthropic format.

---

### User Story 2 - OAuth device flow authentication via CLI (Priority: P1)

The operator runs `llm-proxy auth copilot` to initiate the GitHub OAuth device flow. The command displays a device code and verification URL, the operator visits the URL and authorizes the application, and the CLI stores the resulting tokens to disk for the proxy to use. This command runs independently of the proxy server — it can be run before the provider is configured or while the proxy is already running.

**Why this priority**: Without authentication, no requests can be proxied. The OAuth device flow is the only supported authentication method for the Copilot API, making this equally critical to the core proxy functionality.

**Independent Test**: Can be tested by running `llm-proxy auth copilot`, completing the device flow, and verifying tokens are stored to disk. Then starting the proxy and confirming requests succeed.

**Acceptance Scenarios**:

1. **Given** no stored Copilot credentials, **When** the operator runs `llm-proxy auth copilot`, **Then** the CLI initiates the OAuth device flow and displays the verification URL and user code.
2. **Given** the device flow has been initiated, **When** the operator completes authorization on GitHub, **Then** the CLI obtains and stores access tokens to disk.
3. **Given** stored tokens exist from a previous auth session, **When** the proxy starts with a Copilot provider configured, **Then** it reads and uses the stored tokens without requiring re-authentication.
4. **Given** the stored access token has expired, **When** a request is sent, **Then** the proxy automatically refreshes the token using the stored refresh token.
5. **Given** no stored tokens exist, **When** a Copilot request arrives at the proxy, **Then** the proxy returns an error instructing the operator to run `llm-proxy auth copilot`.

---

### User Story 3 - Automatic header injection (Priority: P2)

The GitHub Copilot provider automatically injects the required editor-simulation headers (`Editor-Version`, `Copilot-Integration-Id`, `editor-plugin-version`, `user-agent`) on every request. The operator does not need to configure these headers manually.

**Why this priority**: These headers are required by the Copilot API but are static implementation details that should be handled transparently by the provider.

**Independent Test**: Can be tested by sending a request through the Copilot provider and inspecting the outgoing HTTP headers to verify all required headers are present.

**Acceptance Scenarios**:

1. **Given** a configured Copilot provider, **When** a request is forwarded upstream, **Then** the request includes the required editor-simulation headers.
2. **Given** the operator has not configured any extra headers, **When** a request is sent, **Then** the provider automatically adds all required headers without operator intervention.

---

### User Story 4 - Token usage tracking and cost logging (Priority: P2)

The proxy extracts token usage information from GitHub Copilot responses and logs it alongside cost data, consistent with how existing providers (Anthropic) track usage.

**Why this priority**: Usage visibility is a core feature of llm-proxy, and Copilot requests should be tracked the same as any other provider.

**Independent Test**: Can be tested by sending requests through the Copilot provider and verifying that token counts and cost estimates appear in the logs and web UI.

**Acceptance Scenarios**:

1. **Given** a successful Copilot response, **When** the response contains usage data, **Then** the proxy logs input and output token counts.
2. **Given** configured pricing for a Copilot model, **When** a request completes, **Then** the cost is calculated and recorded in the database.

---

### User Story 5 - Configuration via YAML (Priority: P3)

The operator configures the GitHub Copilot provider in the standard `config.yaml` file with a provider type of `"github_copilot"`, specifying only the provider name and optional token storage directory. Model mappings use the existing wildcard pattern (e.g., `gc/*`) to route to the Copilot provider.

**Why this priority**: Follows existing configuration patterns and is straightforward once the provider itself is built.

**Independent Test**: Can be tested by creating a config file with a Copilot provider block and verifying the proxy starts and routes requests correctly.

**Acceptance Scenarios**:

1. **Given** a config file with `type: github_copilot`, **When** the proxy starts, **Then** it initializes the Copilot provider and registers it for request routing.
2. **Given** a wildcard model mapping `gc/*` pointing to the Copilot provider, **When** a client requests `gc/claude-opus-4.6`, **Then** the proxy resolves the model to `claude-opus-4.6` on the Copilot provider.

---

### Edge Cases

- What happens when the GitHub device flow times out before the operator authorizes? The proxy should return a clear error and allow retrying.
- What happens when GitHub revokes the OAuth token? The proxy should detect the 401 response and prompt for re-authentication.
- What happens when the Copilot API rate-limits requests? The proxy should forward the rate-limit response (429) to the client transparently.
- What happens when a model name is not available on the operator's Copilot subscription? The proxy should forward the upstream error to the client.
- What happens if the token storage directory is not writable? The proxy should fail startup with a clear error message.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST implement a new provider type `github_copilot` that satisfies the existing provider interface.
- **FR-002**: System MUST provide an `llm-proxy auth copilot` CLI subcommand that performs the OAuth device flow (RFC 8628) independently of the proxy server.
- **FR-002a**: System MUST return a clear error (with instructions to run `llm-proxy auth copilot`) when a Copilot request arrives but no stored tokens exist.
- **FR-003**: System MUST persist OAuth tokens to disk so they survive proxy restarts.
- **FR-004**: System MUST automatically refresh expired access tokens using stored refresh tokens.
- **FR-005**: System MUST inject required headers (`Editor-Version`, `Copilot-Integration-Id`, `editor-plugin-version`, `user-agent`) on all upstream requests.
- **FR-006**: System MUST forward requests to the Copilot API chat completions endpoint in OpenAI chat completion format.
- **FR-006a**: System MUST accept requests for Copilot models on both the `/v1/messages` (Anthropic) and `/v1/chat/completions` (OpenAI) API paths, translating between formats as needed.
- **FR-007**: System MUST support both streaming (SSE) and non-streaming responses from the Copilot API.
- **FR-008**: System MUST extract token usage (input/output tokens) from Copilot responses for logging.
- **FR-009**: System MUST support configuring the token storage directory via the provider config, defaulting to `~/.config/llm-proxy/copilot/`.
- **FR-009a**: System MUST create the token storage directory with 0700 permissions and write token files with 0600 permissions.
- **FR-010**: System MUST manage its own authentication internally (not forwarding client keys to GitHub).

### Key Entities

- **GitHub Copilot Provider**: A provider implementation that handles OAuth authentication, header injection, and request forwarding to the Copilot API. Key attributes: token storage path, OAuth client credentials, cached access token.
- **OAuth Token Store**: Persisted credentials (access token, refresh token, expiry) stored on disk. Allows the proxy to restart without re-authenticating.
- **Copilot Model**: An upstream model available through the Copilot API (e.g., `claude-opus-4.6`, `gpt-5.2`, `gemini-3.1-pro-preview`). Mapped via the existing model list configuration.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Operators can send chat completion requests through GitHub Copilot models and receive valid responses within the same latency bounds as direct Copilot access (less than 500ms additional overhead from the proxy).
- **SC-002**: OAuth device flow authentication completes successfully on first setup, with stored tokens reused across proxy restarts without re-authentication for at least 30 days.
- **SC-003**: All Copilot requests appear in the proxy's request logs with accurate token counts and cost estimates.
- **SC-004**: Both streaming and non-streaming requests work correctly, with streaming responses delivered to clients in real time without buffering.
- **SC-005**: The provider handles authentication failures gracefully — expired tokens are refreshed automatically, and revoked tokens produce clear error messages rather than crashes.

## Assumptions

- The GitHub Copilot API endpoint is stable and continues to accept OpenAI-format requests.
- The required editor-simulation headers remain valid and are not actively blocked by GitHub.
- The operator has an active GitHub Copilot subscription (Individual, Business, or Enterprise) that grants access to the requested models.
- Token storage uses the local filesystem; no external secret management integration is needed for this feature.
- The OAuth device flow uses GitHub's standard device authorization endpoint.
