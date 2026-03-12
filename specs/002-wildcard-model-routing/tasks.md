# Tasks: Wildcard Model Routing

**Input**: Design documents from `/specs/002-wildcard-model-routing/`
**Prerequisites**: plan.md (required), spec.md (required), research.md, data-model.md

**Tests**: Not explicitly requested in the spec. Test tasks are included for the core matching logic because it is the single critical code path and has clear edge cases.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- Single Go project at repository root
- All changes in `internal/config/`
- No new files or packages

---

## Phase 1: User Story 1 + 2 — Wildcard Matching with Precedence (Priority: P1)

**Goal**: Update `FindModel()` to support wildcard model entries (`model_name` ending in `/*`) using the two-pass algorithm (exact first, then wildcard). Exact matches always take priority over wildcards.

**Independent Test**: Configure a wildcard entry `claude_max/*` and an exact entry in the same config, send requests matching both, and verify correct routing and precedence.

- [X] T001 [US1] Implement wildcard detection and prefix extraction in `FindModel()` in `internal/config/config.go`: add two-pass logic per data-model.md — first pass scans for exact match, second pass scans for wildcard entries (ending in `/*`), strips prefix, returns synthesized `ModelMapping` with `UpstreamModel` set to the suffix. Return error if suffix is empty.
- [X] T002 [US1] [US2] Add table-driven tests for `FindModel()` wildcard matching in `internal/config/config_test.go`: cover exact match still works, wildcard match strips prefix correctly, exact match takes priority over wildcard, first wildcard wins when multiple match, empty suffix returns error, nested slashes in suffix are preserved, non-wildcard trailing slash treated as exact match, no match returns error.

**Checkpoint**: `go test -race ./internal/config/` passes. Both US1 and US2 acceptance scenarios are verified by tests.

---

## Phase 2: User Story 3 — Logging and Cost Verification (Priority: P2)

**Goal**: Verify that wildcard-routed requests are logged with the correct `model_requested` (original client name) and `model_upstream` (stripped suffix), and that cost calculation uses the upstream model name.

**Independent Test**: Send a request through a wildcard route and inspect the log entry for correct model names. Verify cost lookup uses the upstream model.

- [X] T003 [US3] Verify logging and cost paths work for wildcard-resolved models — no code changes expected. The Anthropic handler (`internal/proxy/handler.go:115`) already logs `reqBody.Model` as `modelRequested` and `modelMapping.UpstreamModel` as `modelUpstream`. The OpenAI handler (`internal/proxy/openai_handler.go:119`) uses `modelMapping.ModelName` and `modelMapping.UpstreamModel`. Cost calculation (`internal/proxy/handler.go:150`, `internal/proxy/openai_handler.go:148`) already uses `modelUpstream`. Confirm by reading both handlers and documenting that no changes are needed. If a discrepancy is found, fix it.

**Checkpoint**: Both handler paths confirmed correct for wildcard models. US3 acceptance scenarios satisfied.

---

## Phase 3: Polish & Cross-Cutting Concerns

**Purpose**: Documentation and manual validation

- [X] T004 [P] Update `docs/configuration.md` with wildcard model routing: add `type` field to provider examples, document `/*` suffix convention, add wildcard config examples, document precedence rules (exact > wildcard > error)
- [X] T005 [P] Update `specs/002-wildcard-model-routing/quickstart.md` examples if any config format changed during implementation
- [X] T006 Run quickstart.md validation: build binary, create config with wildcard entry, start proxy, send curl request with wildcard-prefixed model, verify correct routing in logs
- [X] T007 Run `gosec ./...` and `govulncheck ./...` — must report zero findings

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (US1+US2)**: No dependencies — can start immediately (existing project, existing package)
- **Phase 2 (US3)**: Depends on Phase 1 (needs FindModel wildcard logic in place to verify end-to-end)
- **Phase 3 (Polish)**: T004 and T005 can run in parallel with Phase 1. T006 and T007 depend on Phase 1+2 completion.

### User Story Dependencies

- **US1 + US2 (P1)**: Combined because both are implemented in the same function (`FindModel`). The two-pass algorithm inherently handles precedence.
- **US3 (P2)**: Verification-only — depends on US1/US2 being implemented so wildcard-resolved `ModelMapping` values flow through handlers.

### Parallel Opportunities

- T004 and T005 (docs) can run in parallel with each other and with Phase 1 implementation
- T001 and T002 are sequential (implement then test, same file)

---

## Implementation Strategy

### MVP First (User Stories 1+2)

1. Implement T001 (FindModel wildcard logic)
2. Implement T002 (tests)
3. **STOP and VALIDATE**: `go test -race ./internal/config/` passes
4. Core feature is complete and both P1 stories are satisfied

### Full Delivery

1. T001 + T002 → Core wildcard routing works
2. T003 → Logging/cost verified (likely zero changes)
3. T004 + T005 → Docs updated
4. T006 → Manual integration test passes
5. T007 → Security scans clean

---

## Notes

- This is a small, focused feature: ~30 lines of new logic in a single function
- No database schema changes
- No new files or packages
- No handler changes — both Anthropic and OpenAI handlers consume `FindModel()` output unchanged
- Backward compatible — existing exact-match configs work identically
