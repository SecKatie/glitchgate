# Research: Key Management UI

**Feature**: 005-key-management-ui | **Date**: 2026-03-11

## Decision 1: Audit Event Storage

**Decision**: Create a dedicated `audit_events` table in SQLite for key lifecycle events.

**Rationale**: The constitution mandates "Compromise Recording" (Principle V). Structured audit data in the database enables future querying, filtering, and UI display. Logging to stdout alone would be lost on restart and harder to query.

**Alternatives considered**:
- **stdout logging only**: Simpler but not queryable, lost on restart. Rejected because it doesn't meet the "reliable mechanisms that document and track" standard.
- **Append to request_logs table**: Wrong semantic fit — request_logs tracks proxy usage, not admin actions. Would pollute existing data model.
- **Separate audit log file**: Adds file management complexity without query benefits. Rejected.

## Decision 2: Label Edit UX Pattern

**Decision**: Use inline editing for labels in the keys table (click label → editable input → save on blur/enter).

**Rationale**: Inline editing is the most efficient UX for a single-field update. It avoids the overhead of a modal or separate page for what is a quick rename operation. HTMX makes this straightforward with `hx-post` on the input.

**Alternatives considered**:
- **Modal dialog**: Overkill for editing a single text field. Adds unnecessary clicks.
- **Separate edit page**: Too heavy for a label rename.
- **Pencil icon → modal**: Extra click without benefit.

## Decision 3: Key Creation UX Pattern

**Decision**: Inline form at the top of the keys page (label input + create button). On success, display the plaintext key in a highlighted alert box with a copy button. The alert is dismissed when the user navigates away or clicks "Done".

**Rationale**: Keeps the user on the same page. The alert pattern makes the plaintext key visually prominent and the one-time-display warning clear. No modal needed since the form is simple (one field).

**Alternatives considered**:
- **Modal for creation**: Adds complexity for a single-field form. Rejected.
- **Separate creation page**: Breaks the single-page workflow. Rejected.

## Decision 4: Revocation Confirmation Pattern

**Decision**: Use a browser `confirm()` dialog triggered by the revoke button. On confirmation, send an HTMX DELETE/POST to revoke and re-render the key list.

**Rationale**: `confirm()` is the simplest confirmation UX, requires zero additional HTML, and is universally understood. For a destructive action that's infrequent and irreversible from the user's perspective, it's appropriate.

**Alternatives considered**:
- **Custom modal confirmation**: More polished but more code for an infrequent action. Could be added later if desired.
- **Two-click (button changes to "Confirm")**: Viable but less clear than a dialog with explicit text.

## Decision 5: Migration Numbering

**Decision**: Use `005_create_audit_events.sql` as the migration filename.

**Rationale**: The existing migrations use sequential numbering (001, 002, 003). The feature branch is 005, so we use 005 for the migration to maintain alignment. Checked that 004 and 005 don't already exist.

**Alternatives considered**:
- **Timestamp-based**: Not the project convention.
- **004_***: Could conflict with spec 004's potential future migrations.

## Decision 6: CLI `keys update` Command Structure

**Decision**: `llm-proxy keys update <prefix> --label "New Label"` following the same pattern as `keys revoke <prefix>`.

**Rationale**: Consistent with the existing CLI pattern where `revoke` takes a prefix as a positional argument. The `--label` flag matches the `create` command's `--label` flag.

**Alternatives considered**:
- **`keys edit`**: Less standard than `update` in CLI conventions.
- **`keys rename`**: Too specific — `update` allows future expansion to other fields.
