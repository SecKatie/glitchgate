# Tasks: OpenAI Responses API Support

**Input**: Design documents from `/specs/010-responses-api-support/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Configuration extension and OpenAI provider package scaffolding

- [X] T001 Extend provider config to support `type: "openai"` and `api_type` field in `internal/config/config.go`
- [X] T002 [P] Create OpenAI provider package with config types in `internal/provider/openai/types.go`
- [X] T003 [P] Add OpenAI model pricing entries to `internal/pricing/calculator.go`
- [X] T004 Register OpenAI provider factory and `/v1/responses` route in `cmd/serve.go`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core types, provider implementation, and translation infrastructure that ALL user stories depend on

**CRITICAL**: No user story work can begin until this phase is complete

- [X] T005 Define Responses API request/response types (ResponsesRequest, ResponsesResponse, InputItem, OutputItem, OutputContent, ResponsesUsage, ResponsesTool, streaming event types) in `internal/translate/responses_types.go`
- [X] T006 Implement OpenAI provider (`Name()`, `AuthMode()`, `APIFormat()`, `SendRequest()`) with support for both `"openai"` and `"responses"` API formats in `internal/provider/openai/client.go`
- [X] T007 Add `"responses"` source format support to request logger in `internal/proxy/logger.go`
- [X] T008 Add `"responses"` format routing support in fallback chain logic in `internal/proxy/handler.go`

**Checkpoint**: Foundation ready — OpenAI provider can send requests, Responses types are defined, format routing is in place

---

## Phase 3: User Story 1 — Responses API Passthrough (Priority: P1) MVP

**Goal**: A client sends Responses API requests to `/v1/responses` and the proxy forwards them to a Responses API upstream, returning the response unchanged with token usage logged.

**Independent Test**: Configure a Responses API upstream, send a Responses API request, verify correct response with token usage in logs.

### Implementation for User Story 1

- [X] T009 [US1] Create Responses API input handler (parse request, authenticate, resolve model, dispatch to upstream) in `internal/proxy/responses_handler.go`
- [X] T010 [US1] Implement Responses API non-streaming passthrough relay (forward request, return response, extract tokens from `usage` field) in `internal/proxy/responses_handler.go`
- [X] T011 [US1] Implement Responses API streaming passthrough relay (`RelayResponsesSSEStream`) that forwards SSE events unchanged and extracts tokens from `response.completed` event in `internal/proxy/stream.go`
- [X] T012 [US1] Handle Responses API error responses (map upstream errors to Responses API error format) in `internal/proxy/responses_handler.go`
- [X] T013 [US1] Add table-driven tests for Responses API passthrough (non-streaming, streaming, tool calls, errors) in `internal/proxy/responses_handler_test.go`

**Checkpoint**: Responses API passthrough works end-to-end — MVP complete

---

## Phase 4: User Story 2 — Responses API Input to Non-Responses Upstreams (Priority: P2)

**Goal**: A client sends Responses API requests, and the proxy translates them to Anthropic or Chat Completions format for non-Responses upstreams, translating responses back to Responses API format.

**Independent Test**: Configure an Anthropic upstream, send a Responses API request, verify correctly formatted Responses API response.

### Implementation for User Story 2

- [X] T014 [P] [US2] Implement `ResponsesToAnthropic()` request translation (input items, instructions, tools, tool_choice, content types, multimodal) in `internal/translate/responses_to_anthropic.go`
- [X] T015 [P] [US2] Implement `ResponsesToOpenAI()` request translation (input items to messages, tools flat→nested, response_format) in `internal/translate/responses_to_openai.go`
- [X] T016 [P] [US2] Implement `AnthropicToResponsesResponse()` response translation (content blocks, tool_use, stop_reason, usage) in `internal/translate/anthropic_to_responses_response.go`
- [X] T017 [P] [US2] Implement `OpenAIToResponsesResponse()` response translation (choices, tool_calls, finish_reason, usage) in `internal/translate/anthropic_to_responses_response.go`
- [X] T018 [US2] Implement streaming translation from Anthropic SSE to Responses API SSE events in `internal/translate/responses_stream_translator.go`
- [X] T019 [US2] Implement streaming translation from Chat Completions SSE to Responses API SSE events in `internal/translate/responses_stream_translator.go`
- [X] T020 [US2] Wire Responses handler to dispatch to non-Responses upstreams with translation in `internal/proxy/responses_handler.go`
- [X] T021 [US2] Handle essential vs optional feature classification: error on untranslatable content-bearing features, silently drop behavioral params in `internal/translate/responses_to_anthropic.go` and `internal/translate/responses_to_openai.go`
- [X] T022 [P] [US2] Add table-driven tests for Responses→Anthropic request/response translation in `internal/translate/responses_to_anthropic_test.go`
- [X] T023 [P] [US2] Add table-driven tests for Responses→Chat Completions request/response translation in `internal/translate/responses_to_openai_test.go`

**Checkpoint**: Responses API clients can reach Anthropic and Chat Completions upstreams

---

## Phase 5: User Story 4 — OpenAI Chat Completions Upstream Provider (Priority: P2)

**Goal**: An operator configures an OpenAI Chat Completions upstream and clients in any format (Anthropic, Chat Completions, Responses API) can route to it.

**Independent Test**: Configure an OpenAI Chat Completions upstream, send an Anthropic-format request, verify correct Anthropic-format response with tokens logged.

### Implementation for User Story 4

- [X] T024 [US4] Ensure OpenAI provider `SendRequest()` correctly handles Chat Completions upstream (endpoint routing, auth header, response parsing) in `internal/provider/openai/client.go`
- [X] T025 [US4] Wire `"openai"` format into existing OpenAI handler for Chat Completions passthrough (avoid double-translation for CC→CC) in `internal/proxy/openai_handler.go`
- [X] T026 [US4] Support `forward` auth mode (use client-provided API key) alongside `proxy_key` mode in `internal/provider/openai/client.go`
- [X] T027 [US4] Add table-driven tests for OpenAI Chat Completions provider (proxy_key auth, forward auth, streaming, token extraction) in `internal/provider/openai/client_test.go`

**Checkpoint**: OpenAI Chat Completions upstream works with all input formats

---

## Phase 6: User Story 3 — Anthropic/CC Input to Responses API Upstream (Priority: P3)

**Goal**: Clients sending Anthropic or Chat Completions format requests can reach a Responses API upstream, with the proxy translating in both directions.

**Independent Test**: Configure a Responses API upstream, send an Anthropic-format request, verify correct Anthropic-format response.

### Implementation for User Story 3

- [X] T028 [P] [US3] Implement `AnthropicToResponses()` request translation (messages, system, tools, tool_choice, content blocks, multimodal) in `internal/translate/anthropic_to_responses.go`
- [X] T029 [P] [US3] Implement `OpenAIToResponses()` request translation (messages to input items, tools nested→flat) in `internal/translate/openai_to_responses.go`
- [X] T030 [P] [US3] Implement `ResponsesToAnthropicResponse()` response translation (output items to content blocks, status to stop_reason, usage) in `internal/translate/responses_to_anthropic_response.go`
- [X] T031 [P] [US3] Implement `ResponsesToOpenAIResponse()` response translation (output items to choices, usage mapping, id prefix) in `internal/translate/responses_to_openai_response.go`
- [X] T032 [US3] Implement streaming translation from Responses API SSE to Anthropic SSE events in `internal/translate/responses_reverse_stream.go`
- [X] T033 [US3] Implement streaming translation from Responses API SSE to Chat Completions SSE events in `internal/translate/responses_reverse_stream.go`
- [X] T034 [US3] Wire Anthropic handler to dispatch to Responses API upstreams with translation in `internal/proxy/handler.go`
- [X] T035 [US3] Wire OpenAI handler to dispatch to Responses API upstreams with translation in `internal/proxy/openai_handler.go`
- [X] T036 [P] [US3] Add table-driven tests for Anthropic→Responses request/response translation in `internal/translate/anthropic_to_responses_test.go`
- [X] T037 [P] [US3] Add table-driven tests for Chat Completions→Responses request/response translation in `internal/translate/openai_to_responses_test.go`

**Checkpoint**: Full 3x3 translation matrix operational

---

## Phase 7: User Story 5 — OpenAI Providers in Fallback Chains (Priority: P3)

**Goal**: OpenAI providers (Chat Completions and Responses API) participate in fallback chains alongside Anthropic providers, retrying on server errors and rate limits with format translation.

**Independent Test**: Configure a fallback chain with a failing provider followed by an OpenAI provider, trigger a 5xx error, verify the request succeeds via fallback.

### Implementation for User Story 5

- [X] T038 [US5] Verify OpenAI provider returns retryable error indicators compatible with existing fallback chain logic in `internal/proxy/handler.go`
- [X] T039 [US5] Add table-driven tests for fallback chains involving OpenAI providers (CC and Responses API) with cross-format translation in `internal/proxy/handler_test.go`

**Checkpoint**: OpenAI providers fully integrated into fallback chains

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: Benchmarks, validation hardening, and integration verification

- [X] T040 [P] Add benchmark tests for all translation hot paths (per constitution requirement) in `internal/translate/benchmark_test.go`
- [X] T041 [P] Add input validation for `/v1/responses` endpoint (model required, input valid JSON, tools unique names, temperature range) in `internal/proxy/responses_handler.go`
- [X] T042 [P] Add multimodal edge case handling: error on `input_file` to non-Responses upstreams, error on `input_audio` to Anthropic upstream in `internal/translate/responses_to_anthropic.go` and `internal/translate/responses_to_openai.go`
- [X] T043 Run `make lint` and `make audit` (golangci-lint, gosec, govulncheck) and fix any findings
- [X] T044 Run quickstart.md validation: verify documented config examples work end-to-end

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — can start immediately
- **Foundational (Phase 2)**: Depends on Phase 1 (T001-T004) — BLOCKS all user stories
- **US1 (Phase 3)**: Depends on Phase 2 — MVP, no other story dependencies
- **US2 (Phase 4)**: Depends on Phase 2 — can run in parallel with US1
- **US4 (Phase 5)**: Depends on Phase 2 — can run in parallel with US1 and US2
- **US3 (Phase 6)**: Depends on Phase 2 — can run in parallel with US1, US2, US4
- **US5 (Phase 7)**: Depends on Phase 2 + at least one OpenAI provider path working (US1 or US4)
- **Polish (Phase 8)**: Depends on all user stories being complete

### User Story Dependencies

- **US1 (P1)**: Start after Phase 2 — no dependencies on other stories
- **US2 (P2)**: Start after Phase 2 — no dependencies on other stories (uses same types from Phase 2)
- **US4 (P2)**: Start after Phase 2 — no dependencies on other stories
- **US3 (P3)**: Start after Phase 2 — no dependencies on other stories
- **US5 (P3)**: Start after Phase 2 — benefits from US1 or US4 being complete for integration testing

### Within Each User Story

- Translation functions before handler wiring
- Non-streaming before streaming
- Request translation before response translation
- Core implementation before tests

### Parallel Opportunities

- T002, T003 can run in parallel (different files)
- T014, T015, T016, T017 can run in parallel (different files, independent translations)
- T028, T029, T030, T031 can run in parallel (different files, independent translations)
- T022, T023 can run in parallel (different test files)
- T036, T037 can run in parallel (different test files)
- T040, T041, T042 can run in parallel (different concerns)
- US1, US2, US4, US3 can all proceed in parallel after Phase 2

---

## Parallel Example: User Story 2

```bash
# Launch all request/response translation functions in parallel (different files):
Task: "Implement ResponsesToAnthropic() in internal/translate/responses_to_anthropic.go"
Task: "Implement ResponsesToOpenAI() in internal/translate/responses_to_openai.go"
Task: "Implement AnthropicToResponsesResponse() in internal/translate/anthropic_to_responses_response.go"
Task: "Implement OpenAIToResponsesResponse() in internal/translate/openai_to_responses_response.go"

# Then sequentially: streaming, handler wiring, tests
```

## Parallel Example: User Story 3

```bash
# Launch all translation functions in parallel (different files):
Task: "Implement AnthropicToResponses() in internal/translate/anthropic_to_responses.go"
Task: "Implement OpenAIToResponses() in internal/translate/openai_to_responses.go"
Task: "Implement ResponsesToAnthropicResponse() in internal/translate/responses_to_anthropic_response.go"
Task: "Implement ResponsesToOpenAIResponse() in internal/translate/responses_to_openai_response.go"

# Then sequentially: streaming, handler wiring, tests
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup (T001-T004)
2. Complete Phase 2: Foundational (T005-T008)
3. Complete Phase 3: US1 Responses Passthrough (T009-T013)
4. **STOP and VALIDATE**: Test Responses API passthrough end-to-end
5. Deploy/demo if ready

### Incremental Delivery

1. Setup + Foundational (T001-T008) → Foundation ready
2. US1: Responses passthrough (T009-T013) → **MVP!**
3. US2: Responses→non-Responses translation (T014-T023) → Responses clients can reach any upstream
4. US4: Chat Completions upstream (T024-T027) → OpenAI CC provider operational
5. US3: Anthropic/CC→Responses upstream (T028-T037) → Full 3x3 matrix
6. US5: Fallback chains (T038-T039) → Complete integration
7. Polish (T040-T044) → Production-ready

### Parallel Team Strategy

With multiple developers:

1. Team completes Setup + Foundational together (T001-T008)
2. Once Foundational is done:
   - Developer A: US1 (Responses passthrough)
   - Developer B: US2 (Responses→non-Responses translation)
   - Developer C: US4 (Chat Completions upstream provider)
3. After initial stories stabilize:
   - Developer A: US3 (Anthropic/CC→Responses upstream)
   - Developer B: US5 (Fallback chains)
   - Developer C: Polish (benchmarks, validation, audit)

---

## Notes

- [P] tasks = different files, no dependencies on incomplete tasks
- [Story] label maps task to specific user story for traceability
- Each user story is independently completable and testable
- Translation functions are pure — no HTTP framework types, independently testable
- Streaming relay must never buffer full responses (constitution requirement)
- Benchmark tests required for all translation hot paths (constitution requirement)
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
