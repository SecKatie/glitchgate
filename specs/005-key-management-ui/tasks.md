# Tasks: Key Management UI

**Input**: Design documents from `/specs/005-key-management-ui/`
**Prerequisites**: plan.md (required), spec.md (required for user stories), research.md, data-model.md, contracts/

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

---

## Phase 1: Setup

**Purpose**: Database migration and store interface additions needed by all user stories

- [X] T001 Create audit_events migration in internal/store/migrations/005_create_audit_events.sql with columns: id (INTEGER PRIMARY KEY AUTOINCREMENT), action (TEXT NOT NULL), key_prefix (TEXT NOT NULL), detail (TEXT), created_at (DATETIME NOT NULL)
- [X] T002 Add AuditEvent struct, UpdateKeyLabel method, and RecordAuditEvent method to Store interface in internal/store/store.go
- [X] T003 Implement UpdateKeyLabel in internal/store/sqlite.go — UPDATE proxy_keys SET label = ? WHERE key_prefix = ? AND revoked_at IS NULL, check RowsAffected, return error if 0 rows
- [X] T004 Implement RecordAuditEvent in internal/store/sqlite.go — INSERT into audit_events with action, key_prefix, detail, created_at=NOW()

---

## Phase 2: Foundational (Navigation + Page Shell)

**Purpose**: Keys page skeleton and navigation link — MUST complete before user story UI work

- [X] T005 Add "Keys" navigation link to internal/web/templates/layout.html between Costs and Logout, with ActiveTab "keys" highlighting using the same aria-current pattern as Logs/Costs
- [X] T006 Create internal/web/templates/keys.html page template extending layout.html — define "title" block as "Keys", "content" block with a placeholder table (thead: Prefix, Label, Created, Actions) and empty tbody with id="key-table-body"
- [X] T007 Create internal/web/templates/fragments/key_rows.html defining a "key_rows" named template — iterate over .Keys rendering table rows with prefix, label, created_at, and an actions cell (placeholder for revoke/edit)
- [X] T008 Add KeysPage handler method to Handlers in internal/web/handlers.go — call store.ListActiveProxyKeys, render keys.html with ActiveTab "keys" and Keys data
- [X] T009 Add KeysAPIHandler method to Handlers in internal/web/handlers.go — call store.ListActiveProxyKeys, return key_rows fragment for HTMX or JSON for non-HTMX requests
- [X] T010 Register routes in cmd/serve.go: GET /ui/keys → KeysPage, GET /ui/api/keys → KeysAPIHandler (inside the authenticated route group)

**Checkpoint**: Keys page loads with navigation link active, displays existing keys in a table (or empty state). All user story work can now begin.

---

## Phase 3: User Story 1 — View Active Keys (Priority: P1) 🎯 MVP

**Goal**: Display all active proxy keys with prefix, label, and creation date, ordered by most recent first. Show empty state when no keys exist.

**Independent Test**: Navigate to /ui/keys and verify active keys display correctly. Create keys via CLI, verify they appear. Revoke via CLI, verify they disappear.

### Implementation for User Story 1

- [X] T011 [US1] Update keys.html content block in internal/web/templates/keys.html to show empty state message ("No API keys yet. Create one below.") when .Keys is empty, using `{{if .Keys}}...{{else}}...{{end}}`
- [X] T012 [US1] Update key_rows fragment in internal/web/templates/fragments/key_rows.html to format created_at as a human-readable date and display prefix in a monospace font

**Checkpoint**: User Story 1 is complete — keys page shows active keys or empty state. MVP is functional.

---

## Phase 4: User Story 2 — Create a New Key (Priority: P1)

**Goal**: Create a new proxy key with a label via the web UI, display plaintext once with copy button and warning.

**Independent Test**: Enter a label in the create form, submit, verify plaintext key is displayed with copy button and warning. Refresh page, verify plaintext is gone but key appears in list.

### Implementation for User Story 2

- [X] T013 [US2] Add create key form to internal/web/templates/keys.html — label input (maxlength=64, required), "Create Key" submit button, using hx-post="/ui/api/keys" hx-target="body" (full page re-render to show alert)
- [X] T014 [US2] Add created-key alert section to internal/web/templates/keys.html — conditionally rendered when .CreatedKey is set, showing plaintext key in a `<code>` block, copy-to-clipboard button (JS navigator.clipboard.writeText), and warning text "Save this key now — it will not be shown again."
- [X] T015 [US2] Add CreateKeyHandler method to Handlers in internal/web/handlers.go — parse label from form, validate non-empty and ≤64 chars, call auth.GenerateKey(), call store.CreateProxyKey(), call store.RecordAuditEvent("key_created", prefix, label), re-render keys.html with CreatedKey/CreatedPrefix/CreatedLabel in template data
- [X] T016 [US2] Register POST /ui/api/keys route in cmd/serve.go pointing to CreateKeyHandler

**Checkpoint**: User Story 2 is complete — keys can be created from the UI with plaintext shown once.

---

## Phase 5: User Story 3 — Edit a Key's Label (Priority: P2)

**Goal**: Edit key labels via inline UI editing and a new CLI `keys update` command.

**Independent Test**: Click a label in the keys table, edit it, press Enter — verify label updates. Run `llm-proxy keys update <prefix> --label "New"` — verify label changes.

### Implementation for User Story 3

- [X] T017 [P] [US3] Update key_rows fragment in internal/web/templates/fragments/key_rows.html to make the label cell clickable — on click, replace with an input field (value=current label, maxlength=64) that posts to /ui/api/keys/{prefix}/update on blur or Enter, using hx-post and hx-target="#key-table-body" hx-swap="innerHTML"
- [X] T018 [P] [US3] Add UpdateKeyLabelHandler method to Handlers in internal/web/handlers.go — extract prefix from chi.URLParam, parse label from form, validate non-empty and ≤64 chars, call store.UpdateKeyLabel(), return updated key_rows fragment for HTMX or JSON
- [X] T019 [US3] Register POST /ui/api/keys/{prefix}/update route in cmd/serve.go pointing to UpdateKeyLabelHandler
- [X] T020 [US3] Add keysUpdateCmd cobra command in cmd/keys.go — positional arg for prefix, --label flag (required, validated ≤64 chars), RunE calls openStore() then store.UpdateKeyLabel(), prints confirmation or error

**Checkpoint**: User Story 3 is complete — labels are editable in both UI and CLI.

---

## Phase 6: User Story 4 — Revoke an Existing Key (Priority: P2)

**Goal**: Revoke keys via the web UI with a confirmation step.

**Independent Test**: Click "Revoke" on a key, confirm in dialog, verify key disappears from list. Try to use the revoked key for a proxy request — verify it's rejected.

### Implementation for User Story 4

- [X] T021 [P] [US4] Update key_rows fragment in internal/web/templates/fragments/key_rows.html to add a "Revoke" button in the actions cell with hx-post="/ui/api/keys/{prefix}/revoke" hx-target="#key-table-body" hx-swap="innerHTML" hx-confirm="Revoke key {prefix}? This cannot be undone."
- [X] T022 [P] [US4] Add RevokeKeyHandler method to Handlers in internal/web/handlers.go — extract prefix from chi.URLParam, call store.RevokeProxyKey(), call store.RecordAuditEvent("key_revoked", prefix, ""), return updated key_rows fragment for HTMX or JSON, return 404 if key not found
- [X] T023 [US4] Register POST /ui/api/keys/{prefix}/revoke route in cmd/serve.go pointing to RevokeKeyHandler

**Checkpoint**: User Story 4 is complete — keys can be revoked from the UI with confirmation.

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Validation edge cases, audit integration in existing CLI commands, final cleanup

- [X] T024 Update existing runKeysCreate in cmd/keys.go to call store.RecordAuditEvent("key_created", prefix, label) after successful key creation
- [X] T025 Update existing runKeysRevoke in cmd/keys.go to call store.RecordAuditEvent("key_revoked", prefix, "") after successful revocation
- [X] T026 Add label validation helper function in internal/web/handlers.go — validateLabel(label string) error, checks non-empty and ≤64 characters, reuse in CreateKeyHandler and UpdateKeyLabelHandler
- [X] T027 Run `make lint` and `make audit` (golangci-lint, gosec, govulncheck) — fix any findings
- [X] T028 Run `make test` — verify all existing tests pass with new code, no regressions

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (Setup)**: No dependencies — start immediately
- **Phase 2 (Foundational)**: Depends on Phase 1 (store methods must exist for handlers)
- **Phase 3 (US1)**: Depends on Phase 2 (page shell must exist)
- **Phase 4 (US2)**: Depends on Phase 2 (page shell must exist). Independent of US1.
- **Phase 5 (US3)**: Depends on Phase 2 (page shell must exist). Independent of US1/US2.
- **Phase 6 (US4)**: Depends on Phase 2 (page shell must exist). Independent of US1/US2/US3.
- **Phase 7 (Polish)**: Depends on all user story phases being complete

### User Story Dependencies

- **US1 (View Keys)**: Phase 2 only — no dependencies on other stories
- **US2 (Create Key)**: Phase 2 only — no dependencies on other stories
- **US3 (Edit Label)**: Phase 2 only — no dependencies on other stories
- **US4 (Revoke Key)**: Phase 2 only — no dependencies on other stories

All user stories can be implemented in parallel after Phase 2 completes.

### Within Each User Story

- Template changes before or parallel with handler changes
- Handler must exist before route registration
- Route registration is the final step

### Parallel Opportunities

- T003 and T004 can run in parallel (different store methods, same file but independent functions)
- T005, T006, T007 can run in parallel (different template files)
- T008 and T009 can run in parallel (different handler methods, same file but independent)
- T017 and T018 can run in parallel (template vs handler, different files)
- T021 and T022 can run in parallel (template vs handler, different files)
- All four user story phases (3-6) can run in parallel after Phase 2

---

## Parallel Example: Phase 2 (Foundational)

```text
# Parallel group 1 (templates — different files):
T005: Add Keys nav link to layout.html
T006: Create keys.html page template
T007: Create key_rows.html fragment

# Parallel group 2 (handlers — can be parallel if careful):
T008: KeysPage handler
T009: KeysAPIHandler

# Sequential (depends on handlers):
T010: Register routes in serve.go
```

## Parallel Example: User Stories after Phase 2

```text
# All stories can run in parallel:
Stream A: T011-T012 (US1: View)
Stream B: T013-T016 (US2: Create)
Stream C: T017-T020 (US3: Edit Label)
Stream D: T021-T023 (US4: Revoke)
```

---

## Implementation Strategy

### MVP First (User Stories 1 + 2)

1. Complete Phase 1: Setup (migrations + store methods)
2. Complete Phase 2: Foundational (page shell + navigation + routes)
3. Complete Phase 3: US1 (view keys with empty state)
4. Complete Phase 4: US2 (create keys)
5. **STOP and VALIDATE**: Can view and create keys entirely in the UI
6. Deploy/demo if ready

### Incremental Delivery

1. Setup + Foundational → Keys page visible with nav link
2. Add US1 (View) → Keys page shows active keys → Deploy
3. Add US2 (Create) → Full create workflow → Deploy (MVP!)
4. Add US3 (Edit Label) → Inline label editing + CLI update command → Deploy
5. Add US4 (Revoke) → Revoke with confirmation → Deploy
6. Polish → Audit logging in CLI, validation cleanup, lint/test → Deploy

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- No test tasks generated (not requested in spec)
- Commit after each phase or logical task group
- Stop at any checkpoint to validate story independently
- The key_rows.html fragment is modified across multiple stories (US1, US3, US4) — if implementing in parallel, merge carefully
