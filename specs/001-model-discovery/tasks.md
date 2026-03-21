# Tasks: Automatic Model Discovery

**Input**: Design documents from `/specs/001-model-discovery/`
**Prerequisites**: plan.md (required), spec.md (required), research.md, data-model.md, quickstart.md

**Tests**: Tests are included — the project constitution requires table-driven tests for all new code.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- **Go project**: `internal/` packages at repository root
- Paths use the existing project structure from plan.md

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Add shared types and config fields that all user stories depend on

- [x] T001 Add `DiscoveredModel` struct and `ModelDiscoverer` interface to `internal/provider/provider.go`
- [x] T002 Add `DiscoverModels`, `ModelPrefix`, and `DiscoverFilter` fields to `ProviderConfig` in `internal/config/config.go`
- [x] T003 Add `discover_models`, `model_prefix`, and `discover_filter` examples to `config.example.yaml`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core discovery orchestration and filter logic that MUST be complete before provider-specific implementations

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [x] T004 Implement `matchDiscoverFilter(modelID string, filters []string) bool` helper function in `internal/config/discovery.go` using `filepath.Match` glob semantics with `!` prefix exclusion support
- [x] T005 Implement `InjectDiscoveredModels(providers map[string]provider.Provider) error` method on `Config` in `internal/config/discovery.go` — iterates providers with `DiscoverModels == true`, type-asserts to `ModelDiscoverer`, calls `ListModels`, applies filter, injects `ModelMapping` entries (skipping explicit duplicates), rebuilds `resolvedChains`
- [x] T006 Add config validation in `internal/config/config.go` `Load()` function: reject `discover_models: true` on unsupported provider types (`github_copilot`) with clear error message
- [x] T007 Wire `InjectDiscoveredModels` call into `NewProviderRegistry` in `internal/app/providers.go` — call after all providers are built, before returning the registry
- [x] T008 [P] Write table-driven tests for `matchDiscoverFilter` in `internal/config/discovery_test.go` — cover: include-only, exclude-only, mixed, empty filter (include all), invalid glob pattern error

**Checkpoint**: Foundation ready - provider-specific ListModels implementations can now proceed in parallel

---

## Phase 3: User Story 1 - Provider with Model Discovery (Priority: P1) 🎯 MVP

**Goal**: A provider with `discover_models: true` auto-populates model routing at startup with correct prefix naming and explicit entry precedence

**Independent Test**: Configure a single provider with `discover_models: true` and verify discovered models appear in `FindModel()` results without explicit `model_list` entries

### Tests for User Story 1

- [x] T009 [P] [US1] Write table-driven tests for `anthropic.Client.ListModels` in `internal/provider/anthropic/client_test.go` — mock HTTP responses matching Anthropic `/v1/models` response shape, test pagination, test auth headers
- [x] T010 [P] [US1] Write table-driven tests for `openai.Client.ListModels` in `internal/provider/openai/client_test.go` — mock HTTP responses matching OpenAI `/v1/models` response shape, test `Authorization: Bearer` header
- [x] T011 [P] [US1] Write table-driven tests for `gemini.Client.ListModels` in `internal/provider/gemini/client_test.go` — mock HTTP responses matching Gemini `/v1beta/models` response shape, test pagination, test `generateContent` filtering, test API key auth
- [x] T012 [P] [US1] Write integration test for `InjectDiscoveredModels` in `internal/config/discovery_test.go` — test full flow: mock provider implementing `ModelDiscoverer`, verify `ModelMapping` entries created with correct prefix, verify `FindModel` resolves discovered models

### Implementation for User Story 1

- [x] T013 [P] [US1] Implement `ListModels(ctx) ([]DiscoveredModel, error)` on `anthropic.Client` in `internal/provider/anthropic/client.go` — call `GET {baseURL}/v1/models` with cursor pagination (`after_id`, `limit=1000`), extract `id` and `display_name` from response `data[]`, handle Vertex mode (`GET https://{region}-aiplatform.googleapis.com/v1beta1/publishers/anthropic/models`)
- [x] T014 [P] [US1] Implement `ListModels(ctx) ([]DiscoveredModel, error)` on `openai.Client` in `internal/provider/openai/client.go` — call `GET {baseURL}/v1/models` (no pagination), extract `id` from response `data[]`
- [x] T015 [P] [US1] Implement `ListModels(ctx) ([]DiscoveredModel, error)` on `gemini.Client` in `internal/provider/gemini/client.go` — call `GET {baseURL}/v1beta/models` with page-token pagination, strip `models/` prefix from `name`, use `displayName`, filter to models with `generateContent` in `supportedGenerationMethods[]`, handle Vertex mode (`GET https://{region}-aiplatform.googleapis.com/v1beta1/publishers/google/models`)
- [x] T016 [US1] Add model listing response types to provider type files: `anthropic/types.go` (ModelsListResponse, ModelInfo), `openai/types.go` (ModelsListResponse, ModelInfo), `gemini/types.go` (ModelsListResponse, GeminiModelInfo) — only fields needed for discovery
- [x] T017 [US1] Write end-to-end test in `internal/config/discovery_test.go` — verify: `model_prefix: "custom/"` produces `custom/model-id` names, empty `model_prefix: ""` produces bare model names, nil `model_prefix` defaults to `{provider-name}/`
- [x] T018 [US1] Write test for explicit precedence in `internal/config/discovery_test.go` — verify: explicit `model_list` entry with same name as discovered model wins, discovered entry is skipped

**Checkpoint**: At this point, a single provider with `discover_models: true` works end-to-end

---

## Phase 4: User Story 2 - No Discovery by Default (Priority: P1)

**Goal**: Existing configurations with no `discover_models` field continue to work unchanged

**Independent Test**: Start server with existing config (no `discover_models` set), verify no provider listing API calls are made and only explicit `model_list` entries exist

### Implementation for User Story 2

- [x] T019 [US2] Write test in `internal/config/discovery_test.go` verifying that `InjectDiscoveredModels` with no providers having `DiscoverModels: true` makes zero `ListModels` calls and leaves `ModelList` unchanged
- [x] T020 [US2] Verify all existing tests pass with `make test` — no behavioral changes to providers without `discover_models` set

**Checkpoint**: Backward compatibility confirmed

---

## Phase 5: User Story 3 - Per-Provider Discovery Control (Priority: P2)

**Goal**: Administrators can enable discovery on some providers and not others, mixing discovered and manually configured models

**Independent Test**: Configure two providers (one with discovery, one without), verify only the discovery-enabled provider's models appear alongside all explicit entries

### Implementation for User Story 3

- [x] T021 [US3] Write test in `internal/config/discovery_test.go` — configure two mock providers (one discovery-enabled, one not), verify only the enabled one's models are injected while explicit entries from both providers remain

**Checkpoint**: Mixed configurations work correctly

---

## Phase 6: User Story 4 - Unsupported Discovery Handling (Priority: P2)

**Goal**: Setting `discover_models: true` on an unsupported provider type (`github_copilot`) fails startup with a clear config validation error

**Independent Test**: Configure a `github_copilot` provider with `discover_models: true`, verify server returns config error and refuses to start

### Implementation for User Story 4

- [x] T022 [US4] Write test in `internal/config/config_test.go` verifying that `Load()` returns error when `github_copilot` provider has `discover_models: true`
- [x] T023 [US4] Write test in `internal/config/config_test.go` verifying that `Load()` succeeds when `github_copilot` provider has `discover_models: false` or unset

**Checkpoint**: Unsupported provider types are properly rejected at config validation

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Improvements that affect multiple user stories

- [x] T024 [P] Add structured logging to `InjectDiscoveredModels` in `internal/config/discovery.go` — log discovered model count per provider, log skipped duplicates, log filter matches, log API failures as warnings
- [x] T025 [P] Update `config.example.yaml` with commented examples showing all discovery options (discover_models, model_prefix, discover_filter with include/exclude patterns)
- [x] T026 Run `make lint` and `make audit` (`gosec` + `govulncheck`) — fix any findings
- [x] T027 Run full test suite with `make test` — verify all existing and new tests pass with race detector
- [x] T028 Validate quickstart.md scenarios manually or via test — verify documented config patterns work

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — can start immediately
- **Foundational (Phase 2)**: Depends on Phase 1 — BLOCKS all user stories
- **User Stories (Phases 3–6)**: All depend on Phase 2 completion
  - US1 (Phase 3) and US2 (Phase 4) are both P1 — US1 first (US2 is a verification pass)
  - US3 (Phase 5) and US4 (Phase 6) are P2 — can proceed after US1/US2
  - US3 and US4 can run in parallel (independent concerns)
- **Polish (Phase 7)**: Depends on all user stories being complete

### User Story Dependencies

- **User Story 1 (P1)**: Can start after Phase 2 — no dependencies on other stories
- **User Story 2 (P1)**: Can start after Phase 2 — validates backward compat (logically after US1 but no code dependency)
- **User Story 3 (P2)**: Can start after Phase 2 — no dependencies on other stories
- **User Story 4 (P2)**: Can start after Phase 2 — config validation is independent of discovery implementation

### Within Each User Story

- Tests MUST be written and FAIL before implementation (T009–T012 before T013–T018)
- Response types (T016) needed by ListModels implementations (T013–T015), but can be parallelized by file
- Integration tests (T012, T017, T018) depend on ListModels implementations

### Parallel Opportunities

- T001, T002, T003 can run in parallel (different files)
- T004 and T008 can run in parallel (implementation + tests, same file but independent logic)
- T009, T010, T011, T012 can all run in parallel (different test files)
- T013, T014, T015 can all run in parallel (different provider packages)
- T022 and T023 can run in parallel with T021 (different test files)
- T024 and T025 can run in parallel (different files)

---

## Parallel Example: User Story 1

```bash
# Launch all provider ListModels tests together:
Task: "T009 — anthropic ListModels tests in internal/provider/anthropic/client_test.go"
Task: "T010 — openai ListModels tests in internal/provider/openai/client_test.go"
Task: "T011 — gemini ListModels tests in internal/provider/gemini/client_test.go"
Task: "T012 — InjectDiscoveredModels integration test in internal/config/discovery_test.go"

# Then launch all provider ListModels implementations together:
Task: "T013 — anthropic ListModels in internal/provider/anthropic/client.go"
Task: "T014 — openai ListModels in internal/provider/openai/client.go"
Task: "T015 — gemini ListModels in internal/provider/gemini/client.go"
Task: "T016 — model listing response types across provider type files"
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup (T001–T003)
2. Complete Phase 2: Foundational (T004–T008)
3. Complete Phase 3: User Story 1 (T009–T018)
4. **STOP and VALIDATE**: Run `make test`, verify single provider discovery works end-to-end
5. Deploy/demo if ready

### Incremental Delivery

1. Phase 1 + Phase 2 → Foundation ready
2. Add US1 → Test independently → Deploy/Demo (MVP!)
3. Add US2 → Verify backward compat → Deploy/Demo
4. Add US3 + US4 → Test independently → Deploy/Demo
5. Polish phase → Final validation → Release

### Parallel Team Strategy

With multiple developers:

1. Team completes Setup + Foundational together
2. Once Foundational is done:
   - Developer A: T013 (Anthropic ListModels) + T009 (tests)
   - Developer B: T014 (OpenAI ListModels) + T010 (tests)
   - Developer C: T015 (Gemini ListModels) + T011 (tests)
3. Integrate and run T012, T017, T018 together
4. US2–US4 can be split across developers

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story should be independently completable and testable
- Verify tests fail before implementing
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- `openai_responses` type uses the same `openai.Client` — no separate ListModels needed
