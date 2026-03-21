# Feature Specification: Automatic Model Discovery

**Feature Branch**: `001-model-discovery`
**Created**: 2026-03-20
**Status**: Draft
**Input**: User description: "Add automatic model discovery for providers that support it. When `discover_models: true` is set on a provider, auto-populate model_list entries. Support `model_prefix` to customize the client-facing model name prefix."

## User Scenarios & Testing

### User Story 1 - Provider with Model Discovery (Priority: P1)

**As an administrator**, I want to configure a provider with `discover_models: true` so that I don't have to manually add every model to `model_list`.

**Why this priority**: This is the core value proposition - eliminates repetitive manual configuration.

**Independent Test**: Can be tested by configuring a provider with discovery enabled and verifying that discovered models appear in the routing table without explicit model_list entries.

**Acceptance Scenarios**:

1. **Given** a provider configured with `discover_models: true`, **When** the server starts, **Then** the system fetches the model list from the provider and creates model mappings for each discovered model.

2. **Given** a provider with `model_prefix: "custom/"`, **When** models are discovered, **Then** client-facing model names use the custom prefix (e.g., `custom/claude-sonnet-4-6`).

3. **Given** an explicit `model_list` entry and a discovered entry with the same model name, **When** there is a conflict, **Then** the explicit `model_list` entry takes precedence.

---

### User Story 2 - No Discovery by Default (Priority: P1)

**As an administrator**, I want discovery to be opt-in so that existing configurations continue to work unchanged.

**Why this priority**: Backward compatibility is critical - existing deployments must not be affected.

**Independent Test**: Can be tested by starting a server with a provider that has no `discover_models` set, and verifying no discovery occurs.

**Acceptance Scenarios**:

1. **Given** a provider configured without `discover_models`, **When** the server starts, **Then** no model discovery occurs and only explicit `model_list` entries are available.

---

### User Story 3 - Per-Provider Discovery Control (Priority: P2)

**As an administrator**, I want to enable discovery on some providers and not others so that I can mix discovered and manually configured models.

**Why this priority**: Allows gradual migration and hybrid configurations.

**Acceptance Scenarios**:

1. **Given** two providers where one has `discover_models: true` and one has `discover_models: false`, **When** both are configured, **Then** only the first provider discovers models automatically.

---

### User Story 4 - Unsupported Discovery Handling (Priority: P2)

**As an administrator**, I want the system to fail fast with a clear error when a provider doesn't support discovery, so that I can fix configuration issues quickly.

**Why this priority**: Prevents silent failures that are hard to debug.

**Acceptance Scenarios**:

1. **Given** a `github_copilot` provider (which has no model listing API) with `discover_models: true`, **When** the server starts, **Then** a clear error is returned indicating the provider doesn't support model discovery.

2. **Given** a provider type that doesn't support discovery, **When** `discover_models: true` is set, **Then** the system logs a warning and treats `discover_models: false`.

---

## Requirements

### Functional Requirements

- **FR-001**: System MUST support `discover_models: true|false` in `ProviderConfig` (defaults to `false`)
- **FR-002**: System MUST support `model_prefix` string in `ProviderConfig` (defaults to `{provider-name}/`)
- **FR-003**: When `discover_models: true`, system MUST call provider's model listing endpoint at startup
- **FR-004**: Discovered models MUST be added to `resolvedChains` with format `{model_prefix}{upstream_model}`
- **FR-005**: Explicit `model_list` entries MUST take precedence over discovered entries
- **FR-006**: Providers that don't support model discovery MUST either fail with clear error OR log warning and disable discovery
- **FR-007**: Discovery MUST support `anthropic`, `openai`, `openai_responses`, `gemini` provider types
- **FR-008**: Discovery MUST NOT be supported for `github_copilot` provider type

### Key Entities

- **DiscoveredModel**: Represents a single model from a provider's listing endpoint - contains `upstream_model` (the ID the provider uses), `display_name` (optional), and `supported_modes` (chat, vision, etc.)
- **ProviderDiscoveryResult**: Collection of `DiscoveredModel` plus provider metadata
- **ModelMapping (existing)**: Extended to support discovery-sourced entries with `source: "discovered"|"explicit"`

### API Endpoints (Not Applicable)

This feature does not add new public HTTP endpoints. Model discovery is an internal configuration-time operation.

---

## Success Criteria

### Measurable Outcomes

- **SC-001**: A provider with `discover_models: true` correctly populates model routing without any explicit `model_list` entries
- **SC-002**: `model_prefix: "custom/"` produces client-facing names like `custom/model-id`
- **SC-003**: Empty `model_prefix: ""` produces client-facing names equal to the upstream model name
- **SC-004**: Server startup with `discover_models: true` on an unsupported provider returns a clear error within 5 seconds
- **SC-005**: Mixed configuration (some providers with discovery, some without) works correctly
- **SC-006**: Discovery adds no more than 2 seconds to server startup time
- **SC-007**: All existing tests continue to pass
