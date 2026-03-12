# Feature Specification: Fallback Models

**Feature Branch**: `008-model-fallback`
**Created**: 2026-03-11
**Status**: Draft
**Input**: User description: "I want to make a fallback models feature. I want to be able to configure a model in glitchgate that is served by one or more other models in a prioritized list where if the preferred falls over we fallback on the next one"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Configure a Virtual Model (Priority: P1)

An operator adds an entry to the existing `model_list` that, instead of naming a provider and upstream model directly, names an ordered list of other model entries to try. Clients call the virtual model name as they would any other model — they need no knowledge of the fallback chain behind it.

**Why this priority**: This is the core deliverable. Without configuration support, nothing else is possible.

**Independent Test**: Start glitchgate with a `model_list` entry that uses `fallbacks` instead of `provider`/`upstream_model`, verify the server starts and accepts the virtual model name as a valid identifier on both endpoints.

**Acceptance Scenarios**:

1. **Given** a `model_list` entry with a `fallbacks` array referencing two existing model names, **When** glitchgate starts, **Then** it starts without error and treats the virtual model name as a valid model identifier.
2. **Given** a `model_list` entry with a single-entry `fallbacks` array, **When** glitchgate starts, **Then** it starts without error and routes requests for that name directly to the one referenced model.
3. **Given** a `model_list` entry with a `fallbacks` array referencing a model name that does not exist in `model_list`, **When** glitchgate starts, **Then** startup fails with a clear validation error naming the unknown reference.
4. **Given** a `model_list` entry with both `provider` and `fallbacks` fields set, **When** glitchgate starts, **Then** startup fails with a clear validation error indicating the two forms are mutually exclusive.
5. **Given** a `model_list` entry with a `fallbacks` array that directly or indirectly references itself, **When** glitchgate starts, **Then** startup fails with a clear validation error identifying the cycle.

---

### User Story 2 - Transparent Fallback on Primary Failure (Priority: P1)

A client sends a request using a virtual model name. The first model in the fallback list fails. Glitchgate resolves the next entry and retries transparently, returning the first successful response to the client.

**Why this priority**: This is the runtime behaviour that delivers the feature's value.

**Independent Test**: Configure a virtual model whose first fallback entry always returns a 5xx; send a request and verify a successful response from the second entry is returned to the client.

**Acceptance Scenarios**:

1. **Given** a virtual model with two fallback entries and the first returns a 5xx, **When** a client sends a non-streaming request, **Then** glitchgate retries against the second entry and returns its successful response.
2. **Given** a virtual model with two fallback entries and the first connection fails (timeout, refused), **When** a client sends a request, **Then** glitchgate retries against the second entry.
3. **Given** a virtual model with two fallback entries and the first returns a 5xx, **When** a client sends a streaming request, **Then** glitchgate retries against the second entry — no partial data from the failed attempt reaches the client.
4. **Given** a virtual model where all fallback entries fail, **When** a client sends a request, **Then** glitchgate returns a service unavailable error to the client.
5. **Given** a virtual model whose first fallback entry is itself another virtual model, **When** the outer virtual model is requested and its first entry (the inner virtual) fails entirely, **Then** glitchgate moves on to the next entry in the outer chain.
6. **Given** a successful first-entry response, **When** a client sends a request, **Then** glitchgate returns that response immediately without trying subsequent entries.
7. **Given** a provider returns a 4xx error other than 429, **When** a client sends a request via a virtual model, **Then** glitchgate does NOT attempt fallback and returns the 4xx to the client.

---

### User Story 3 - Observability: Which Model Actually Served the Request (Priority: P2)

Every request log entry records the actual provider and upstream model that served the response, along with the number of attempts made before success or exhaustion.

**Why this priority**: Operators need to know when fallback is occurring so they can investigate degraded providers.

**Independent Test**: Send a request that triggers one fallback; check the log entry and confirm it records the second entry's provider, upstream model, and an attempt count of 2.

**Acceptance Scenarios**:

1. **Given** a request served by the first fallback entry, **When** the log is viewed, **Then** it shows that entry's actual provider, upstream model, and attempt count of 1.
2. **Given** a request that fell back to the second entry, **When** the log is viewed, **Then** it shows the second entry's provider and model with attempt count of 2.
3. **Given** a request that exhausted all entries, **When** the log is viewed, **Then** it records the total attempt count and that no provider succeeded.

---

### Edge Cases

- What happens when streaming has started and the upstream drops mid-response? Fallback is not possible once data has been forwarded to the client; the client connection is terminated with an error.
- What happens when a provider returns a 5xx on a streaming request before any SSE data is forwarded? The failure is detected before the client response is committed; fallback proceeds and the client receives no partial data.
- What happens when the same model name appears more than once in a `fallbacks` array? Duplicate entries are allowed — the operator may intentionally retry the same model.
- What happens when a virtual model's `fallbacks` entry is itself a virtual model that also fails completely? The outer chain treats the inner virtual model as a single failed attempt and moves to the next entry.
- What happens with the request body on retry? The full original request body is replayed unchanged to each entry; no state from a failed attempt is carried forward.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The `model_list` configuration MUST support two mutually exclusive entry forms: a direct entry (specifying `provider` and `upstream_model`) and a virtual entry (specifying a `fallbacks` array of model names). An entry with both forms set MUST be rejected at startup.
- **FR-002**: A virtual entry's `fallbacks` array MUST contain at least one model name. A single-entry array is valid and provides pure name-indirection with no fallback behaviour.
- **FR-003**: Every model name referenced in a `fallbacks` array MUST resolve to another entry in `model_list` (direct or virtual); unknown references MUST cause a startup error.
- **FR-004**: The system MUST detect circular references among virtual entries at startup and reject them with a clear error identifying the cycle.
- **FR-005**: When a request arrives for a virtual model, the system MUST resolve and attempt the first entry in its `fallbacks` array first. If that entry is itself a virtual model, its full chain is attempted before moving to the next entry in the outer chain.
- **FR-006**: The system MUST treat a provider-model attempt as failed and eligible for fallback when it returns a 5xx HTTP status code or a network-level error (timeout, connection refused).
- **FR-007**: The system MUST NOT trigger fallback for 4xx HTTP errors, with the exception that 429 Too Many Requests MUST trigger fallback, as it indicates provider-side rate limiting rather than a problem with the request itself.
- **FR-008**: On failure of one entry, the system MUST attempt the next entry in the `fallbacks` array in order, replaying the original request body, until a success or the array is exhausted.
- **FR-009**: When all entries in the chain fail, the system MUST return a service unavailable error to the client.
- **FR-010**: For streaming requests, fallback MUST only be attempted if the failure is detected before any response data has been forwarded to the client; once data forwarding has begun, the client connection is terminated with an error.
- **FR-011**: The fallback mechanism MUST apply to both the Anthropic-format endpoint and the OpenAI-compatible endpoint.
- **FR-012**: Every request served via a virtual model MUST record in the request log: the actual provider used, the actual upstream model used, and the total number of provider-model attempts made.
- **FR-013**: The request log schema MUST be extended with an attempt count field; existing log entries default to 1.

### Key Entities

- **Virtual Model Entry**: A `model_list` entry that carries a `fallbacks` array instead of a direct `provider`/`upstream_model`. Indistinguishable from a direct model name from the client's perspective.
- **Direct Model Entry**: A `model_list` entry with a specific `provider` and `upstream_model`. The existing form.
- **Fallback Chain**: The ordered sequence of model names attached to a virtual entry. Entries are resolved and attempted in order; the first success wins. Entries may be direct or virtual.
- **Attempt**: One resolved provider-model dispatch within a request. The attempt count equals the number of dispatches before success or full exhaustion.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Clients using a virtual model name experience no failures due to provider outages when a healthy fallback entry is available — 100% of requests with a viable fallback succeed from the client's perspective.
- **SC-002**: Fallback entry selection adds no measurable overhead beyond the time spent waiting for the failing provider; the selection mechanism itself is imperceptible relative to network round-trip time.
- **SC-003**: Every request log entry for a virtual model accurately records which provider and upstream model served the response and how many attempts were made — 100% log accuracy.
- **SC-004**: Invalid configurations (unknown references, cycles, conflicting entry forms) are caught at startup — 0 invalid configurations silently accepted at runtime.
- **SC-005**: Operators can determine from logs alone, without restarts or config changes, how often fallback is occurring and which providers are degraded.
- **SC-006**: Both the Anthropic-format and OpenAI-format request paths benefit equally from fallback.

## Assumptions

- **A-001**: Fallback is purely reactive — there is no proactive health-checking. Providers are discovered to be down only when a request to them fails.
- **A-002**: Fallback is triggered by 5xx responses, network errors, and 429 Too Many Requests. All other 4xx errors are not eligible for fallback.
- **A-003**: A virtual model name is indistinguishable from a direct model name from the client's perspective — the same API surface, same request format, same response format.
- **A-004**: No per-attempt timeout configuration is introduced in this spec; existing provider timeout settings apply to each attempt individually.
- **A-005**: The full original request body is replayed unchanged to each entry; there is no mutation between attempts.
