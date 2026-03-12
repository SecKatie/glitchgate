# Tasks: Fallback Models

**Input**: Design documents from `/specs/008-model-fallback/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Organization**: Tasks are grouped by user story to enable independent implementation and testing.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1, US2, US3)

---

## Phase 1: Setup (Migration)

**Purpose**: Add the new database column before any code that depends on it is written.

- [X] T001 Add migration `014_add_fallback_attempts.sql` — `ALTER TABLE request_logs ADD COLUMN fallback_attempts INTEGER NOT NULL DEFAULT 1` with goose Up/Down markers in `internal/store/migrations/014_add_fallback_attempts.sql`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Struct changes and store wiring that all user stories depend on.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

- [X] T002 Add `Fallbacks []string` field (mapstructure/yaml tags) to `ModelMapping` struct in `internal/config/config.go`
- [X] T003 [P] Add `FallbackAttempts int64` to `RequestLogEntry` and `RequestLogSummary` structs in `internal/store/store.go`
- [X] T004 Update `InsertRequestLog` INSERT statement to write `fallback_attempts` and update `ListRequestLogs` / `GetRequestLog` SELECT statements to read it in `internal/store/sqlite.go`

**Checkpoint**: Struct changes and store layer ready — user story work can now begin.

---

## Phase 3: User Story 1 — Configure a Virtual Model (Priority: P1) 🎯 MVP

**Goal**: Operators can define virtual model entries in `model_list` with a `fallbacks` array; glitchgate validates and flattens them at startup.

**Independent Test**: Start glitchgate with a config containing virtual model entries (single-entry, multi-entry, nested virtual). Verify it starts without error. Then supply a config with an unknown reference, a cycle, or both `provider` and `fallbacks` set, and verify startup fails with a clear error message.

### Implementation for User Story 1

- [X] T005 [US1] Add load-time validation that each `ModelMapping` has either (`provider` + `upstream_model`) OR `fallbacks`, but not both and not neither — `internal/config/config.go`
- [X] T006 [US1] Add load-time validation that every name in `fallbacks` exists in `model_list` — `internal/config/config.go`
- [X] T007 [US1] Add DFS cycle detection over virtual entries at load time; return a descriptive error naming the cycle — `internal/config/config.go`
- [X] T008 [US1] Add unexported `resolvedChains map[string][]ModelMapping` field to `Config`; populate it at load time by flattening each virtual model's chain recursively (virtual-in-virtual expands in place) — `internal/config/config.go`
- [X] T009 [US1] Update `FindModel` to return `([]ModelMapping, error)`: check `resolvedChains` first (direct entry → slice of one, virtual entry → flattened slice), then fall through to existing wildcard logic for direct entries not in the map — `internal/config/config.go`
- [X] T010 [P] [US1] Update `Handler.ServeHTTP` in `internal/proxy/handler.go` to compile with the new `FindModel` signature — temporary pass-through using `chain[0]`; the full retry loop is added in Phase 4
- [X] T011 [P] [US1] Update `OpenAIHandler.ServeHTTP` in `internal/proxy/openai_handler.go` to compile with the new `FindModel` signature — same temporary pass-through
- [X] T012 [P] [US1] Write table-driven tests in `internal/config/config_test.go` covering: mutual exclusivity validation, unknown `fallbacks` reference rejected, cycle detected and named, single-entry chain valid, multi-entry chain flattened correctly, nested virtual flattened correctly, `FindModel` returns correct `[]ModelMapping` for direct and virtual entries

**Checkpoint**: Config validation, flattening, and `FindModel` are complete and tested. Both handlers compile. Glitchgate starts correctly with virtual model configs. User Story 1 is independently verifiable.

---

## Phase 4: User Story 2 — Transparent Fallback on Primary Failure (Priority: P1)

**Goal**: At runtime, requests to a virtual model automatically retry through the fallback chain on 5xx, 429, or network error; the client sees only the first successful response.

**Independent Test**: Configure a virtual model whose first entry always returns a 5xx (mock or stub provider). Send a request and verify the second entry's successful response is returned. Verify a 4xx (non-429) from the first entry is returned immediately without retry.

### Implementation for User Story 2

- [X] T013 [US2] Add `isFallbackStatus(code int) bool` helper (returns `code >= 500 || code == 429`) in `internal/proxy/handler.go`
- [X] T014 [US2] Add `attemptCount int` parameter to `logRequest` on `Handler`; write it to `RequestLogEntry.FallbackAttempts` — `internal/proxy/handler.go`
- [X] T015 [US2] Replace the pass-through in `Handler.ServeHTTP` with a retry loop: iterate over `chain`, call `SendRequest` per entry, on `err != nil` or `isFallbackStatus(provResp.StatusCode)` close any open stream and continue; on exhaustion log all attempts and return 503; on success dispatch to existing `handleStreaming`/`handleNonStreaming` — `internal/proxy/handler.go`
- [X] T016 [US2] Update all `logRequest` call sites within `internal/proxy/handler.go` to pass the correct attempt count (loop index + 1)
- [X] T017 [P] [US2] Mirror T013–T016 in `OpenAIHandler`: add `isFallbackStatus` usage, `attemptCount` parameter to its `logRequest` calls, and the same retry loop — `internal/proxy/openai_handler.go`
- [X] T018 [P] [US2] Write table-driven tests in `internal/proxy/handler_test.go` covering: 5xx triggers retry to second entry, 429 triggers retry, non-429 4xx returns immediately without retry, all entries exhausted returns 503, first-entry success short-circuits, streaming request falls back when 5xx received before any bytes forwarded to client
- [X] T019 [P] [US2] Write table-driven tests in `internal/proxy/openai_handler_test.go` covering the same scenarios for the OpenAI-format endpoint

**Checkpoint**: Fallback runtime behaviour is fully implemented and tested on both endpoints. User Story 2 is independently verifiable.

---

## Phase 5: User Story 3 — Observability (Priority: P2)

**Goal**: Every request log entry for a virtual model records the actual provider/model used and the number of attempts made.

**Independent Test**: Send a request that triggers exactly one fallback. Retrieve the log entry and confirm `fallback_attempts` equals 2, `provider_name` names the second entry's provider, and `model_upstream` names its upstream model.

### Implementation for User Story 3

- [X] T020 [US3] Write tests in `internal/proxy/handler_test.go` verifying `RequestLogEntry.FallbackAttempts` is 1 for a first-entry success, 2 after one fallback, and equals chain length when all entries are exhausted
- [X] T021 [P] [US3] Write tests in `internal/proxy/openai_handler_test.go` verifying the same `FallbackAttempts` values for the OpenAI-format endpoint

**Checkpoint**: Observability is verified. All three user stories complete.

---

## Phase 6: Polish & Cross-Cutting Concerns

- [X] T022 [P] Update `docs/configuration.md` with `fallbacks` field documentation and YAML examples from `specs/008-model-fallback/quickstart.md`
- [X] T023 [P] Run `make test` (`go test -race ./...`) and fix any failures
- [X] T024 [P] Run `make lint` (`golangci-lint run`) and resolve all findings
- [X] T025 [P] Run `make audit` (`gosec` + `govulncheck`) and resolve all findings

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1** (Setup): No dependencies — start immediately
- **Phase 2** (Foundational): Depends on Phase 1 — blocks all user stories
- **Phase 3** (US1): Depends on Phase 2 — config layer; T010/T011 can run in parallel once T009 is done
- **Phase 4** (US2): Depends on Phase 3 — requires `FindModel` returning `[]ModelMapping` and `resolvedChains` populated
- **Phase 5** (US3): Depends on Phase 4 — verifies observability wired in Phase 4
- **Phase 6** (Polish): Depends on Phase 5

### User Story Dependencies

- **US1**: Can start after Foundational — no dependency on US2 or US3
- **US2**: Depends on US1 (`FindModel` must return `[]ModelMapping`) — no dependency on US3
- **US3**: Depends on US2 (`attemptCount` must flow through `logRequest`)

### Within Each Phase

- T010 and T011 are parallel (different files)
- T017 and T018/T019 are parallel (different files)
- T020 and T021 are parallel (different files)
- All Phase 6 tasks are parallel

---

## Parallel Opportunities

```
# Phase 2: run in parallel after T001
T003 (store structs)       ← no dependency on T002
T002 (ModelMapping field)  ← independent

# Phase 3: after T009 (FindModel updated)
T010 handler.go pass-through   ← parallel with T011
T011 openai_handler.go         ← parallel with T010
T012 config tests              ← parallel with T010, T011

# Phase 4: after T015/T016 (retry loop in Handler done)
T017 openai_handler retry      ← parallel with T018, T019
T018 handler tests             ← parallel with T017, T019
T019 openai handler tests      ← parallel with T017, T018

# Phase 5
T020 handler observability tests     ← parallel with T021
T021 openai observability tests      ← parallel with T020
```

---

## Implementation Strategy

### MVP (User Stories 1 + 2 only)

1. Complete Phase 1: Migration
2. Complete Phase 2: Foundational struct changes
3. Complete Phase 3: US1 — config validation, flattening, `FindModel`
4. Complete Phase 4: US2 — handler retry loop
5. **STOP and VALIDATE**: virtual models route correctly; fallback fires on 5xx/429/network error
6. Ship — US3 observability can follow

### Incremental Delivery

1. Phase 1+2 → struct changes land, migration in place
2. Phase 3 → operators can define virtual models; config validates at startup
3. Phase 4 → runtime fallback works end-to-end
4. Phase 5 → attempt counts visible in logs
5. Phase 6 → docs, lint, audit clean

---

## Notes

- `[P]` tasks touch different files and have no incomplete dependencies — safe to run concurrently
- T010/T011 are intentionally temporary (pass-through) — replaced by T015/T017
- The `isFallbackStatus` helper (T013) is defined once in `handler.go` and used by both handlers; `openai_handler.go` calls it directly since they are in the same package
- Wildcard model resolution (`/*` suffix in `model_name`) is unchanged — only direct entries support wildcards; virtual entries do not
