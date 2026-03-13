# Feature Specification: Models Page

**Feature Branch**: `011-models-page`
**Created**: 2026-03-13
**Status**: Draft
**Input**: User description: "a new page in the webui called Models. It must list the configured models, their pricing info, and allow a detail page to be opened. The detail page should list things like pricing, usage, provider, example curl request, etc."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Browse Configured Models (Priority: P1)

An operator navigates to the Models section of the web UI and sees a list of all models configured in the system. For each model, they can see the model name, its provider, and a summary of its pricing rates. This gives a quick overview of what models are available to clients connecting to the proxy.

**Why this priority**: The list page is the entry point for all model-related information. Without it, the detail page is inaccessible and the feature delivers no value.

**Independent Test**: Can be fully tested by navigating to the Models page and verifying that configured models appear with their provider and pricing summary; delivers immediate operational visibility.

**Acceptance Scenarios**:

1. **Given** a glitchgate instance with multiple models in `model_list`, **When** the operator visits the Models page, **Then** each configured model appears as a row with its name, associated provider name, and input/output pricing rates.
2. **Given** a model that is a virtual/fallback chain (no direct provider), **When** it appears in the list, **Then** it is visually distinguishable as a virtual model and shows its fallback chain members.
3. **Given** a model with custom pricing overrides in its metadata, **When** it appears in the list, **Then** the overridden rates are shown (not the defaults).
4. **Given** no models are configured, **When** the operator visits the Models page, **Then** an empty-state message is shown instead of a blank table.

---

### User Story 2 - View Model Detail (Priority: P2)

An operator clicks on a model in the list and is taken to a detail page for that model. The detail page shows comprehensive information: full pricing breakdown (input, output, cache read, cache write), cumulative usage statistics (total requests, total tokens consumed, total cost incurred), provider details, and an example `curl` command they can copy to test that model through the proxy.

**Why this priority**: The detail page is the primary value-add over the existing Logs and Costs pages — it consolidates pricing reference, usage at a glance, and onboarding help (example curl) in one place.

**Independent Test**: Can be fully tested by clicking any model in the list and verifying the detail page renders pricing, usage totals, provider info, and a copyable curl example.

**Acceptance Scenarios**:

1. **Given** a model with known pricing, **When** the operator opens its detail page, **Then** input, output, cache read, and cache write rates per million tokens are all displayed.
2. **Given** a model that has processed requests, **When** the operator opens its detail page, **Then** the page shows total request count, total input tokens, total output tokens, and total cost attributed to that model.
3. **Given** any configured model, **When** the operator opens its detail page, **Then** an example `curl` command is shown demonstrating how to call the proxy using that model name via the Messages API.
4. **Given** a model with a specific provider, **When** the operator views the detail page, **Then** the provider name and provider type (e.g., Anthropic, OpenAI, GitHub Copilot) are shown.
5. **Given** a virtual/fallback model, **When** the operator views its detail page, **Then** the ordered fallback chain is shown with each member model listed.
6. **Given** a model that has never been used, **When** the operator views its detail page, **Then** usage statistics show zero values rather than errors or blank fields.

---

### User Story 3 - Navigate Between List and Detail (Priority: P3)

The operator can move fluidly between the model list and a model's detail page using standard navigation (browser back button and in-page links), consistent with how the rest of the web UI works.

**Why this priority**: Navigation quality is a polish concern; the core value is already delivered by P1 and P2.

**Independent Test**: Can be tested by verifying that clicking a model opens its detail page and a back link returns to the list.

**Acceptance Scenarios**:

1. **Given** the operator is on the Models list, **When** they click a model name or row, **Then** they navigate to that model's detail page.
2. **Given** the operator is on a model detail page, **When** they use the back link, **Then** they return to the Models list.

---

### Edge Cases

- What happens when a model exists in the config but has no pricing data (e.g., a GitHub Copilot model with no known per-token rates)? Display a clear "N/A" indicator rather than zero.
- What happens if the proxy has no API keys configured — the example `curl` command still renders with a visible placeholder key value.
- How does the page behave if the config contains a wildcard model entry (e.g., `gc/*`) — it appears as a single row representing the wildcard pattern.
- A model may appear in request logs (historical usage) but have since been removed from the config — the Models page shows only currently configured models; historical-only models are not listed.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST provide a Models page accessible via the main navigation of the web UI.
- **FR-002**: The Models page MUST display all models explicitly listed in the `model_list` configuration.
- **FR-003**: Each model row on the list MUST display: model name, provider name (or "Virtual" for fallback-only models), and input/output pricing rates per million tokens.
- **FR-004**: Models with custom pricing metadata MUST display their overridden rates, not the provider defaults.
- **FR-005**: Models with no known pricing (no defaults and no metadata overrides) MUST display a clear indicator (e.g., "—") rather than a zero or blank.
- **FR-006**: Each model in the list MUST be a link that navigates to a dedicated model detail page.
- **FR-007**: The model detail page MUST display all pricing tiers: input, output, cache read, and cache write rates per million tokens.
- **FR-008**: The model detail page MUST display cumulative usage statistics for that model: total request count, total input tokens, total output tokens, and total cost.
- **FR-009**: The model detail page MUST display provider information: provider name and provider type.
- **FR-010**: The model detail page MUST display an example `curl` command targeting the proxy's Messages API endpoint using that model's name, with a placeholder API key.
- **FR-011**: For virtual/fallback models, the detail page MUST list the ordered fallback chain.
- **FR-012**: Wildcard model entries (e.g., `gc/*`) MUST appear in the model list as a single row representing the wildcard pattern.
- **FR-013**: The model detail page MUST include a back-navigation link that returns the user to the model list.
- **FR-014**: The example `curl` command MUST use a placeholder value for the API key so no real credentials are exposed.

### Key Entities

- **Model**: A named entry in `model_list`; has a name, optional provider + upstream mapping, optional fallback chain, and optional pricing metadata. Central entity for this feature.
- **Provider**: An upstream LLM endpoint defined in the config; has a name and type (Anthropic, OpenAI, GitHub Copilot, etc.).
- **Pricing Entry**: The set of per-million-token rates (input, output, cache read, cache write) for a model — sourced from either built-in defaults or per-model metadata overrides.
- **Model Usage Summary**: Aggregated usage statistics for a model derived from request logs: total request count, total input tokens, total output tokens, and total cost.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can find and view any configured model's full pricing and provider details in three or fewer clicks from the main navigation.
- **SC-002**: The model detail page renders all sections (pricing, usage, provider, example curl) in a single page view without requiring additional navigation or expansion.
- **SC-003**: The example `curl` command on the detail page is syntactically correct and can be used (after substituting a real key) to make a successful request to the proxy.
- **SC-004**: All models in the config's `model_list` are represented on the Models list page — no configured model is silently omitted.
- **SC-005**: The feature introduces no regressions to the existing Logs, Costs, or Keys pages.

## Assumptions

- The example `curl` command will target the Anthropic-compatible Messages API (`/v1/messages`) as the primary protocol; a note can indicate that OpenAI-compatible paths are also supported.
- Usage statistics are aggregated from the existing request logs using the model name — no new data collection is required.
- Wildcard entries (e.g., `gc/*`) are shown as a single row in the list since they represent a routing pattern, not a fixed model name.
- Models removed from the config but still in historical logs are not shown on the Models page; their historical usage remains accessible via the Logs and Costs pages.
- The web UI follows the existing HTMX + Pico CSS pattern; this feature follows the same conventions.
