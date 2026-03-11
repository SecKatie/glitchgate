# Feature Specification: Wildcard Model Routing

**Feature Branch**: `002-wildcard-model-routing`
**Created**: 2026-03-11
**Status**: Draft
**Input**: User description: "a feature where we can define a model with a prefix like `claude_max/*` so that any model matched against that prefix will get the prefix stripped and the model on the end will get sent as the upstream model name"

## User Scenarios & Testing

### User Story 1 - Wildcard prefix model routing (Priority: P1)

As a proxy operator, I want to define a single model mapping entry with a wildcard prefix (e.g., `claude_max/*`) so that any model name starting with that prefix is automatically routed to the correct provider with the prefix stripped, without needing to enumerate every upstream model individually.

Today, routing `claude-sonnet-4-20250514` and `claude-opus-4-20250514` through a "claude-max" provider requires two separate `model_list` entries. With wildcard routing, a single `claude_max/*` entry handles both — and any future models — automatically.

**Why this priority**: This is the core feature. Without wildcard matching, no other stories matter.

**Independent Test**: Can be fully tested by configuring a wildcard model entry in the config file, sending a request with a matching model name, and verifying the correct provider receives the request with the prefix stripped.

**Acceptance Scenarios**:

1. **Given** a model mapping `claude_max/*` pointing to provider "claude-max", **When** a client sends a request with `model: "claude_max/claude-sonnet-4-20250514"`, **Then** the request is routed to the "claude-max" provider with `model: "claude-sonnet-4-20250514"`.
2. **Given** a model mapping `claude_max/*` pointing to provider "claude-max", **When** a client sends a request with `model: "claude_max/claude-opus-4-20250514"`, **Then** the request is routed to the "claude-max" provider with `model: "claude-opus-4-20250514"`.
3. **Given** a model mapping `claude_max/*` pointing to provider "claude-max", **When** a client sends a request with `model: "claude_max/"` (empty model after prefix), **Then** the request is rejected with a clear error indicating the model name is invalid.

---

### User Story 2 - Exact matches take priority over wildcards (Priority: P1)

As a proxy operator, I want exact model mappings to take priority over wildcard matches so that I can override specific models while using wildcards as a catch-all for everything else.

**Why this priority**: Without clear precedence rules, operators cannot safely combine wildcards with explicit overrides, which is a common and expected configuration pattern.

**Independent Test**: Can be tested by configuring both an exact model mapping and a wildcard that would also match the same model name, then verifying the exact match wins.

**Acceptance Scenarios**:

1. **Given** an exact mapping for `claude_max/claude-sonnet-4-20250514` pointing to provider "anthropic" AND a wildcard mapping `claude_max/*` pointing to provider "claude-max", **When** a client sends `model: "claude_max/claude-sonnet-4-20250514"`, **Then** the request is routed to the "anthropic" provider (exact match wins).
2. **Given** the same config as above, **When** a client sends `model: "claude_max/claude-opus-4-20250514"`, **Then** the request is routed to the "claude-max" provider via the wildcard (no exact match exists).

---

### User Story 3 - Wildcard models appear in logs and cost tracking (Priority: P2)

As a proxy operator, I want wildcard-routed requests to be logged with both the original client-facing model name and the resolved upstream model name so that I can accurately track usage and costs.

**Why this priority**: Logging and cost tracking are essential for the proxy's value proposition, but are secondary to the routing itself working correctly.

**Independent Test**: Can be tested by sending a request through a wildcard route and verifying the log entry contains the original requested model name and the resolved upstream model name.

**Acceptance Scenarios**:

1. **Given** a wildcard mapping `claude_max/*` to provider "claude-max", **When** a client sends `model: "claude_max/claude-sonnet-4-20250514"`, **Then** the request log records `model_requested: "claude_max/claude-sonnet-4-20250514"` and `model_upstream: "claude-sonnet-4-20250514"`.
2. **Given** the same wildcard route, **When** cost is calculated for the request, **Then** the cost lookup uses the upstream model name (`claude-sonnet-4-20250514`) since that is what pricing is based on.

---

### Edge Cases

- What happens when a model name matches multiple wildcard patterns? The first matching wildcard in config order wins.
- What happens when no wildcard or exact match is found? The existing "model not found" error is returned, unchanged.
- What happens when the wildcard separator character (`/`) appears in the suffix (e.g., `claude_max/org/model-name`)? Only the first prefix segment before `/*` is stripped; the remainder (`org/model-name`) is sent as the upstream model.
- What happens when a wildcard entry has a trailing slash but no `*` (e.g., `claude_max/`)? This is treated as an exact match, not a wildcard. Only the `/*` suffix activates wildcard behavior.

## Requirements

### Functional Requirements

- **FR-001**: System MUST support wildcard model mappings in the `model_list` configuration, identified by a `model_name` ending in `/*`.
- **FR-002**: When a client request's model name matches a wildcard prefix, the system MUST strip the prefix (everything up to and including the `/`) and send the remainder as the upstream model name.
- **FR-003**: Exact model name matches MUST take priority over wildcard matches.
- **FR-004**: When multiple wildcard patterns match a model name, the first match in configuration order MUST win.
- **FR-005**: The system MUST reject requests where a wildcard matches but the suffix after the prefix is empty, returning an appropriate error message.
- **FR-006**: Wildcard-routed requests MUST be logged with the original client-facing model name as `model_requested` and the stripped suffix as `model_upstream`.
- **FR-007**: Cost calculation for wildcard-routed requests MUST use the resolved upstream model name for pricing lookups.
- **FR-008**: The `upstream_model` field in a wildcard model mapping entry MUST be ignored (the upstream model is derived from the client request, not from config).
- **FR-009**: Existing exact-match model routing MUST continue to work unchanged (backward compatible).

### Key Entities

- **Wildcard Model Mapping**: A `model_list` entry where `model_name` ends with `/*`. The prefix before `/*` is the match pattern. The `provider` field determines which upstream provider handles the request. The `upstream_model` field is unused.
- **Resolved Model**: The portion of the client's requested model name after the wildcard prefix and separator are stripped. This becomes the upstream model name.

## Assumptions

- The wildcard delimiter is `/` (forward slash), consistent with the user's example of `claude_max/*`.
- Only suffix wildcards are supported (`prefix/*`). No infix or regex patterns.
- The `upstream_model` field in a wildcard entry is ignored — it may be left empty or set to any value without effect.
- Wildcard entries do not need to validate that the resolved upstream model actually exists at the provider. The upstream provider will return its own error if the model is invalid.

## Success Criteria

### Measurable Outcomes

- **SC-001**: A proxy operator can configure a single wildcard model entry and successfully route requests for any model matching the prefix, without adding per-model config entries.
- **SC-002**: Exact model mappings always take precedence over wildcards — 100% of requests matching an exact entry are routed via the exact entry, never the wildcard.
- **SC-003**: All wildcard-routed requests are logged with correct model names and costed using the resolved upstream model, with no data loss or misattribution.
- **SC-004**: Existing configurations with only exact model mappings continue to work identically after this feature is added (zero regressions).
