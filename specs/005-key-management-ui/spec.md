# Feature Specification: Key Management UI

**Feature Branch**: `005-key-management-ui`
**Created**: 2026-03-11
**Status**: Draft
**Input**: User description: "a key management UI to replicate the cli featureset."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - View Active Keys (Priority: P1)

As an administrator, I want to see all active proxy keys in the web UI so that I can understand which keys are in use without switching to the command line.

**Why this priority**: Viewing existing keys is the foundational capability — every other key management action depends on being able to see the current state. This is also the most frequently performed operation.

**Independent Test**: Can be fully tested by navigating to the keys page and verifying active keys are displayed with their prefix, label, and creation date. Delivers immediate value by providing visibility into active keys.

**Acceptance Scenarios**:

1. **Given** there are active proxy keys, **When** the user navigates to the keys page, **Then** all active keys are listed showing prefix, label, and creation date, ordered by most recent first.
2. **Given** there are no active proxy keys, **When** the user navigates to the keys page, **Then** an empty state message is displayed indicating no keys exist.
3. **Given** some keys have been revoked, **When** the user views the keys page, **Then** only active (non-revoked) keys are displayed. Revoked keys are not visible anywhere in the UI.

---

### User Story 2 - Create a New Key (Priority: P1)

As an administrator, I want to create a new proxy key with a label through the web UI so that I can provision access for new consumers without using the CLI.

**Why this priority**: Creating keys is the primary write operation and essential for provisioning access. Without this, the UI cannot replace the CLI for key management.

**Independent Test**: Can be fully tested by creating a key with a label, verifying the plaintext key is displayed once, and confirming the key appears in the active keys list afterward.

**Acceptance Scenarios**:

1. **Given** the user is on the keys page, **When** they provide a label and submit the create form, **Then** a new key is generated and the full plaintext key is displayed exactly once.
2. **Given** a new key has just been created, **When** the plaintext key is displayed, **Then** the user can copy it to their clipboard with a single action.
3. **Given** a new key has just been created, **When** the user navigates away from the creation confirmation, **Then** the plaintext key is no longer retrievable anywhere in the UI.
4. **Given** the user attempts to create a key without a label, **When** they submit the form, **Then** a validation error is shown requiring a label.

---

### User Story 3 - Edit a Key's Label (Priority: P2)

As an administrator, I want to rename a key's label through both the web UI and the CLI so that I can keep key descriptions accurate as their usage evolves, regardless of which interface I prefer.

**Why this priority**: Labels are the primary human-readable identifier for keys. Being able to update them keeps the keys list meaningful over time.

**Independent Test**: Can be fully tested by editing an existing key's label via the UI or CLI and verifying the new label is persisted and displayed in both interfaces.

**Acceptance Scenarios**:

1. **Given** there is an active key, **When** the user edits its label via the web UI and saves, **Then** the updated label is displayed immediately in the keys list.
2. **Given** there is an active key, **When** the user runs the CLI command to update its label by prefix, **Then** the label is updated and a confirmation message is displayed.
3. **Given** the user edits a label to match another key's label, **When** they save, **Then** the change is accepted (labels are not required to be unique).
4. **Given** the user attempts to save an empty label, **When** they submit, **Then** a validation error is shown requiring a non-empty label.

---

### User Story 4 - Revoke an Existing Key (Priority: P2)

As an administrator, I want to revoke a proxy key through the web UI so that I can immediately disable access for a compromised or retired key.

**Why this priority**: Revoking keys is a critical security operation. While less frequent than viewing, the ability to quickly revoke a key is essential for incident response.

**Independent Test**: Can be fully tested by revoking a key from the keys page, confirming the revocation, and verifying the key no longer appears in the active list.

**Acceptance Scenarios**:

1. **Given** there is an active key, **When** the user initiates revocation for that key, **Then** a confirmation step is presented before the key is revoked.
2. **Given** the user confirms key revocation, **When** the revocation completes, **Then** the key is immediately removed from the active keys list.
3. **Given** a key has been revoked, **When** a consumer attempts to use that key for proxy requests, **Then** the request is rejected as unauthorized.

---

### User Story 5 - Navigate to Keys from Any Page (Priority: P2)

As an administrator, I want a "Keys" link in the navigation so that I can access key management from anywhere in the UI.

**Why this priority**: Discoverability is important — the key management page must be easily reachable from the existing navigation pattern used by Logs and Costs pages.

**Independent Test**: Can be fully tested by clicking the Keys navigation link from any page and verifying it loads the keys management page.

**Acceptance Scenarios**:

1. **Given** the user is on any authenticated page, **When** they look at the navigation, **Then** a "Keys" link is visible alongside Logs and Costs.
2. **Given** the user clicks the Keys navigation link, **When** the page loads, **Then** the keys management page is displayed with the Keys tab highlighted as active.

---

### Edge Cases

- What happens when the user creates a key with a very long label? The system should enforce a reasonable maximum label length (64 characters).
- What happens if two administrators try to revoke the same key simultaneously? The second revocation should be handled gracefully (idempotent operation or informative message).
- What happens if the user refreshes the page immediately after key creation? The plaintext key should not be re-displayed — only the active keys list is shown.
- What happens if the user's session expires while on the keys page? They should be redirected to the login page, consistent with other protected pages.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST display a list of all active proxy keys showing prefix, label, and creation date.
- **FR-002**: System MUST order active keys by creation date, most recent first.
- **FR-003**: System MUST allow users to create a new proxy key by providing a human-readable label.
- **FR-004**: System MUST display the full plaintext key exactly once immediately after creation.
- **FR-005**: System MUST provide a copy-to-clipboard action for the newly created plaintext key.
- **FR-006**: System MUST NOT allow the plaintext key to be retrieved or displayed after the initial creation view.
- **FR-007**: System MUST require a non-empty label when creating a key.
- **FR-008**: System MUST allow users to revoke an active key by its prefix.
- **FR-009**: System MUST require explicit confirmation before revoking a key.
- **FR-010**: System MUST immediately remove revoked keys from the active keys display.
- **FR-011**: System MUST include a "Keys" link in the main navigation, consistent with existing navigation items.
- **FR-012**: System MUST restrict key management operations to authenticated users only (same session-based auth as other UI pages).
- **FR-013**: System MUST display an appropriate empty state when no active keys exist.
- **FR-014**: System MUST enforce a maximum label length of 64 characters.
- **FR-015**: System MUST display a clear warning that the plaintext key cannot be shown again after creation.
- **FR-016**: System MUST log key creation and revocation events to an audit trail, recording the action performed, the key prefix, and the timestamp.
- **FR-017**: System MUST allow users to edit a key's label at any time without affecting the key's functionality or prefix, via both the web UI and the CLI.
- **FR-018**: System MUST NOT enforce label uniqueness — multiple active keys may share the same label.
- **FR-019**: The CLI MUST provide a command to update a key's label by prefix (e.g., `llm-proxy keys update <prefix> --label "New Label"`).

### Key Entities

- **Proxy Key**: Represents an authentication credential for the proxy service. Attributes: unique prefix (short identifier visible to users), human-readable label, creation timestamp, revocation timestamp (null if active). The plaintext key is generated once and never stored — only a one-way hash is persisted.
- **Key Summary**: A safe projection of a proxy key for display purposes, containing only the prefix, label, and creation date — never the hash or full key value.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Administrators can view, create, and revoke proxy keys entirely through the web UI without needing the CLI.
- **SC-002**: Key creation workflow (open form, enter label, submit, copy key) completes in under 30 seconds.
- **SC-003**: Key revocation workflow (initiate, confirm) completes in under 10 seconds.
- **SC-004**: The keys page loads and displays all active keys within 2 seconds under normal conditions.
- **SC-005**: 100% of key management operations are available in both the UI and CLI (create, list, revoke, edit label).
- **SC-006**: The plaintext key is never persisted or retrievable after the initial creation display.

## Clarifications

### Session 2026-03-11

- Q: Should key management actions (create, revoke) be logged for audit purposes? → A: Yes — log key create/revoke events (who, when, which key prefix) to an audit trail.
- Q: Should revoked keys be visible in the UI? → A: No — only active keys are shown. Revocation is presented to the user as deletion. Revoked keys remain in the database for audit but are never surfaced in the UI.
- Q: Must key labels be unique, and can they be edited? → A: Labels are not unique — users rely on prefix to distinguish keys. Labels are editable at any time via both UI and CLI.

## Assumptions

- The web UI authentication model (session-based with master key login) is sufficient for key management operations — no additional authorization layer is needed.
- The existing navigation pattern (horizontal tabs: Logs, Costs) will be extended with a "Keys" tab using the same visual style.
- The UI will use the same technology stack as the existing Logs and Costs pages for consistency.
- Label length maximum of 64 characters is reasonable and aligns with typical use (e.g., "Production API Key", "CI/CD Pipeline").
- Revoked keys do not need to be displayed in the UI — the CLI's behavior of only showing active keys will be replicated. Revoked keys remain in the database for audit purposes but are not surfaced in the web interface.
