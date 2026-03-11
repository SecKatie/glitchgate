# Feature Specification: UI Log Improvements

**Feature Branch**: `004-ui-log-improvements`
**Created**: 2026-03-11
**Status**: Draft
**Input**: User description: "Let's see if we can improve the UI. Two things to note right off the bat: Request Logs do not auto-refresh and when viewing a request it is difficult to visually parse through the Request body and Response body. I would like to be able to see the most recent prompt, the response (which could be text, a tool call, etc.), and a hidden details view that I can open to view previous user and agent turns that are in that request. There may be other niceties that I haven't thought of and I am relying on you to help make this good."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Live Log Monitoring (Priority: P1)

An operator has the Logs page open while the proxy is actively handling requests. They want to see new requests appear without needing to manually reload the page, so they can monitor activity in real time.

**Why this priority**: This is the most impactful improvement for day-to-day operation. The logs page is a monitoring surface; without auto-refresh it requires constant manual intervention, defeating its purpose.

**Independent Test**: Open the Logs page, send a new request through the proxy, and confirm the new row appears in the table within a few seconds without a full page reload.

**Acceptance Scenarios**:

1. **Given** the Logs page is open, **When** a new request is proxied, **Then** the new log row appears in the table automatically within 10 seconds.
2. **Given** auto-refresh is active and a filter is applied, **When** a new request matching the filter arrives, **Then** only matching rows update without resetting the filter state.
3. **Given** auto-refresh is active, **When** the user is viewing page 2 of results, **Then** the page does not jump back to page 1 during a refresh cycle.
4. **Given** auto-refresh detects new entries while the user is on page 2 or later, **When** the refresh completes, **Then** a dismissible banner appears indicating the number of new entries with a link to page 1, without auto-navigating.
4. **Given** the user wants to pause auto-refresh, **When** they toggle the refresh control off, **Then** the table stops updating automatically.

---

### User Story 2 - Structured Conversation Viewer (Priority: P2)

A developer opens a log entry to debug why the AI gave an unexpected response. Instead of scanning through a large raw JSON blob, they immediately see the most recent user message and the AI's response in a clean, readable layout. If the request included earlier conversation turns, they can expand a collapsible section to review the full conversation history.

**Why this priority**: Debugging conversations is the primary reason someone drills into a log detail. The current raw JSON view requires manual parsing of deeply nested structures and is the most painful interaction in the current UI.

**Independent Test**: Open any multi-turn log entry detail page and verify the most recent user message and AI response are rendered as readable prose/structured content at the top, with a disclosure control to view prior turns.

**Acceptance Scenarios**:

1. **Given** a log entry with a single-turn request, **When** the detail page is opened, **Then** the user message and AI response are displayed as readable, labelled sections rather than raw JSON.
2. **Given** a log entry with multiple conversation turns, **When** the detail page is opened, **Then** the most recent user message and AI response are shown prominently and prior turns are hidden behind a collapsed disclosure.
3. **Given** a log entry where the AI response is a tool call (function invocation), **When** viewing the response section, **Then** the tool name, arguments, and any tool result are displayed in a structured, labelled format rather than raw JSON.
4. **Given** the prior-turns disclosure is collapsed, **When** the user clicks to expand it, **Then** each prior turn is shown with a clear label (User / Assistant / System) and its content.
5. **Given** a log entry with an error response, **When** viewing the response section, **Then** the error message is displayed clearly, not buried in raw JSON.

---

### User Story 3 - Pretty-Printed Raw Bodies (Priority: P3)

When the conversation viewer cannot interpret a request or response body (e.g., an unknown format or a provider-level error), the raw JSON is displayed formatted with indentation rather than as a compact string, making it easier to read.

**Why this priority**: Serves as a fallback for any request type not covered by the structured viewer, and improves readability with minimal effort.

**Independent Test**: Open a log entry for any request type and confirm the raw body view is indented and not displayed as a single compressed line.

**Acceptance Scenarios**:

1. **Given** any log entry, **When** the raw body view is shown, **Then** JSON is formatted with consistent indentation.
2. **Given** a very large body, **When** the raw view is shown, **Then** the display area is scrollable and does not overflow the page.

---

### User Story 4 - UX Polish and Navigation (Priority: P4)

An operator navigating the UI notices several quality-of-life improvements: the active page is highlighted in the navigation bar, the total number of matching log entries is visible on the logs list, long request IDs can be copied with a single click, and the UI respects the OS dark/light mode preference.

**Why this priority**: These are individually small changes but collectively reduce friction in routine workflows. They also address rough edges that are immediately noticeable.

**Independent Test**: Navigate between Logs and Costs pages and confirm the active page is highlighted. Filter logs and confirm a total count is shown. Use the copy button on an ID field and confirm the value is on the clipboard.

**Acceptance Scenarios**:

1. **Given** the user is on the Logs page, **When** the nav renders, **Then** the "Logs" link is visually distinguished as the active page.
2. **Given** a filtered or unfiltered log list, **When** the list renders, **Then** the total number of matching entries is displayed near the pagination controls.
3. **Given** the detail page is open, **When** the user clicks a copy control next to the request ID, **Then** the ID is copied to the clipboard.
4. **Given** the user's OS is in dark mode, **When** the UI is opened, **Then** the UI respects the OS preference rather than forcing light mode.

---

### User Story 5 - Token and Cost Breakdown (Priority: P3)

An operator reviewing a log entry wants to immediately see the cost and token consumption at a glance, with a drill-down available to understand exactly how those costs break down — including the portion attributable to cached tokens (which are billed at a different rate) versus uncached input tokens.

**Why this priority**: Token cost is the primary financial metric of the proxy. The current detail page buries cost and token counts in a dense metadata grid alongside unrelated fields. Making cost a headline figure and providing a one-click breakdown enables quick cost auditing without requiring the operator to do mental arithmetic.

**Independent Test**: Open any log entry detail page and confirm cost and total token counts are visible without scrolling. Click the details disclosure and confirm the cache token breakdown and per-category cost contributions are shown.

**Acceptance Scenarios**:

1. **Given** a log entry detail page, **When** it renders, **Then** the estimated cost, total input tokens, and output tokens are displayed as prominent top-line figures.
2. **Given** the token details disclosure is collapsed, **When** the user clicks to expand it, **Then** the breakdown shows: uncached input tokens, cache write tokens, cache read tokens, output tokens, and the cost contribution of each category.
3. **Given** a request that used cached tokens, **When** the cost breakdown is shown, **Then** the cached token costs are clearly distinguished from uncached input costs (cache reads and writes are priced differently).
4. **Given** a request with no cache token usage, **When** the cost breakdown is shown, **Then** cache rows show zero without causing an error or layout break.
5. **Given** a request where cost is unknown (e.g., unrecognized model), **When** the top-line cost is shown, **Then** it displays "Unknown" rather than $0.00 to avoid misleading the operator.

---

### Edge Cases

- What happens when a request body is not valid JSON (e.g., a malformed upstream error)? The structured viewer falls back to displaying the raw body as plain text with an indicator that the body could not be parsed.
- What happens when the messages array in a request is empty? The viewer shows a "No messages" placeholder in the conversation section.
- What happens when a content block contains binary or very large data (e.g., a base64-encoded image)? The viewer shows a type label (e.g., "[image content]") without rendering the raw data.
- What happens if auto-refresh fires while the user is actively filling out the filter form? The form state is preserved and not reset mid-edit.
- What happens if the log entry is for a non-chat request type (e.g., a raw completion or an embeddings call)? The structured viewer falls back to the pretty-printed raw body without error.
- What happens if the clipboard API is unavailable (e.g., non-HTTPS context)? The copy control is hidden or shows a graceful fallback (e.g., selecting the text).
- What happens when cost is unknown (unrecognized model pricing)? The top-line cost shows "Unknown" and the cost breakdown section omits per-category cost figures rather than showing $0.00 values.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The Logs list MUST automatically poll for new entries and update the visible table without a full page reload.
- **FR-001a**: The Logs list MUST display a "Last updated X seconds ago" status indicator. If the most recent poll failed, the indicator MUST change to a warning color; polling MUST continue retrying automatically regardless of failure.
- **FR-002**: The auto-refresh interval MUST default to 10 seconds and the user MUST be able to pause/resume auto-refresh from the Logs page.
- **FR-003**: Auto-refresh MUST preserve the current filter parameters, active page, and not disrupt any in-progress form interaction.
- **FR-003a**: When auto-refresh detects new entries and the user is on page 2 or later, a dismissible banner MUST appear showing the count of new entries with a link to page 1. Auto-navigation MUST NOT occur.
- **FR-004**: The log detail view MUST parse the request messages array and display the most recent user-role message prominently, labelled as "Latest Prompt."
- **FR-004a**: If the request includes a top-level `system` field, it MUST be displayed in a dedicated collapsible section labelled "System Prompt," positioned above the conversation view and collapsed by default.
- **FR-005**: The log detail view MUST display the most recent assistant response prominently, labelled as "Response," with support for text content and tool-use content blocks.
- **FR-006**: Tool-use content blocks in the response MUST display the tool name and input arguments in a labelled, structured layout. If a corresponding tool result exists in a subsequent user turn, it MUST also be shown.
- **FR-007**: All conversation turns prior to the most recent user message MUST be placed in a collapsible section labelled "Conversation History," collapsed by default. System-role messages within the messages array (as opposed to the top-level `system` field) are included here.
- **FR-008**: Each turn in the conversation history MUST be clearly labelled with its role (User / Assistant / System) and its content rendered in a readable format. Turn content exceeding ~500 characters MUST be truncated with a "Show more" inline expand control; the same truncation applies to the Latest Prompt and Response sections.
- **FR-009**: When the request body cannot be parsed as a chat-format messages array, the detail view MUST fall back to displaying the body as pretty-printed JSON without an error state.
- **FR-009a**: Every log detail page MUST provide a "View Raw JSON" toggle that reveals the full, pretty-printed request and response bodies regardless of whether the structured viewer successfully parsed the entry.
- **FR-010**: All JSON bodies (request and response) shown in raw view MUST be formatted with indentation rather than compact/minified output.
- **FR-011**: The active navigation item (Logs or Costs) MUST be visually highlighted to indicate the current page.
- **FR-012**: The Logs list MUST display the total count of matching entries alongside or near the pagination controls.
- **FR-013**: The log detail page MUST provide a one-click copy control next to the request ID.
- **FR-014**: The UI MUST respect the operating system's dark/light mode preference rather than hardcoding the light theme.
- **FR-015**: The log detail page MUST display estimated cost, total input tokens, and output tokens as prominent top-line figures, visually distinct from secondary metadata.
- **FR-016**: The log detail page MUST provide a collapsible "Token Details" section containing: uncached input tokens, cache write tokens, cache read tokens, output tokens, and the estimated cost contribution of each category.
- **FR-017**: When cost is unknown (e.g., unrecognized model pricing), the top-line cost MUST display "Unknown" and per-category cost figures in the breakdown MUST be omitted rather than shown as $0.00.

### Key Entities

- **Log Entry**: A single proxied request record with metadata (ID, timestamp, model, status, token counts, cost) and the stored request and response bodies as JSON strings.
- **Conversation Turn**: A single message within the messages array of a chat request, identified by its role (user, assistant, system) and content, which may be a plain string or an array of typed content blocks.
- **Content Block**: A typed unit within a message, such as a text block, a tool-use block (tool name + arguments), or a tool-result block (linked to a prior tool call).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: New log entries appear on the Logs list within 10 seconds of being created, without any user action.
- **SC-002**: An operator can identify the latest user prompt and AI response for any log entry in under 10 seconds without needing to read raw JSON.
- **SC-003**: All JSON bodies shown on the detail page are human-readable without requiring the user to copy and paste into an external formatter.
- **SC-004**: The current active page is identifiable from the navigation bar at a glance, with no ambiguity about which section is selected.
- **SC-005**: The total number of log entries matching the active filter is always visible on the logs list without requiring additional navigation.
- **SC-006**: An operator can see the estimated cost and total token counts for any log entry without scrolling or expanding anything on the detail page.
- **SC-007**: An operator can determine the exact cost contribution of cached vs. uncached tokens for any log entry in one click.

## Clarifications

### Session 2026-03-11

- Q: How should the top-level `system` field on an Anthropic API request be rendered in the structured conversation viewer? → A: Dedicated collapsible "System Prompt" section above the conversation, collapsed by default.
- Q: What should happen when an auto-refresh polling request fails (network error or server error)? → A: Show a small "Last updated X seconds ago" indicator that turns to a warning color if the last poll failed; continue retrying automatically.
- Q: Should raw JSON access be available on log detail pages where the structured viewer successfully parses the entry? → A: Yes — always provide a "View Raw JSON" toggle on every detail page regardless of parse success.
- Q: When auto-refresh detects new entries while the user is on page 2+, what should happen? → A: Show a dismissible "N new entries — view latest" banner linking to page 1 without auto-navigating.
- Q: Should long message content in the conversation viewer be truncated? → A: Yes — truncate each turn's content after ~500 characters with a "Show more" inline expand control.

## Assumptions

- The request body stored in the database follows the Anthropic Messages API structure (`{"model": "...", "messages": [...], ...}`). OpenAI-format requests are translated before logging, so the stored body is always in Anthropic format.
- The structured conversation viewer needs to handle text and tool-use content block types. Image and document content blocks can be indicated by a descriptive label (e.g., "[image content]") without rendering the raw data.
- Auto-refresh applies only to the Logs list page, not the Costs dashboard, as cost summaries are less time-sensitive.
- The copy-to-clipboard feature relies on the browser's native Clipboard API, which is available on modern browsers when accessed over HTTPS or localhost.
- The dark mode behavior uses the OS-level preference signal, not a user-controlled in-app toggle (a toggle can be added in a future iteration).
