# Tasks: GitHub Copilot Provider

**Input**: Design documents from `/specs/006-github-copilot-provider/`
**Prerequisites**: plan.md (required), spec.md (required for user stories), research.md, data-model.md, contracts/

**Tests**: Not explicitly requested in the feature specification. Tests omitted.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Phase 1: Setup

**Purpose**: Create package structure for the Copilot provider

- [X] T001 Create Copilot provider package directory at internal/provider/copilot/

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Extend the Provider interface and config to support OpenAI-native providers. MUST be complete before ANY user story.

- [X] T002 Add `APIFormat() string` method to Provider interface in internal/provider/provider.go
- [X] T003 Add `APIFormat()` returning `"anthropic"` to Anthropic client in internal/provider/anthropic/client.go
- [X] T004 Add `TokenDir` field to ProviderConfig struct in internal/config/config.go
- [X] T005 [P] Define Copilot-specific types (GitHubToken, CopilotSessionToken, DeviceFlowResponse) in internal/provider/copilot/types.go

**Checkpoint**: Foundation ready — Provider interface extended, config supports token_dir, Copilot types defined

---

## Phase 3: User Story 2 - OAuth Device Flow Auth via CLI (Priority: P1)

**Goal**: Operator can run `llm-proxy auth copilot` to authenticate with GitHub and store tokens to disk

**Independent Test**: Run `llm-proxy auth copilot`, complete device flow in browser, verify `~/.config/llm-proxy/copilot/github_token.json` and `copilot_token.json` are created with 0600 permissions

### Implementation for User Story 2

- [X] T006 [US2] Implement token file persistence (read/write JSON with 0600 file, 0700 directory permissions) in internal/provider/copilot/token.go
- [X] T007 [US2] Implement GitHub OAuth device flow (device code request + polling for access token) in internal/provider/copilot/oauth.go
- [X] T008 [US2] Implement Copilot session token exchange (GitHub token → Copilot API token via api.github.com/copilot_internal/v2/token) in internal/provider/copilot/oauth.go
- [X] T009 [US2] Create `auth` parent command in cmd/auth.go
- [X] T010 [US2] Create `auth copilot` subcommand with --token-dir flag in cmd/auth_copilot.go — calls device flow, stores both tokens, handles already-authenticated case

**Checkpoint**: `llm-proxy auth copilot` works end-to-end. Tokens persisted to disk.

---

## Phase 4: User Story 1 - Proxy Requests Through Copilot (Priority: P1) + User Story 3 - Header Injection (Priority: P2)

**Goal**: Clients can send chat completion requests through Copilot models via both `/v1/chat/completions` and `/v1/messages` endpoints. Required headers injected automatically.

**Independent Test**: Configure a Copilot provider in config.yaml, send an OpenAI-format request to `/v1/chat/completions` for model `gc/claude-sonnet-4.6`, verify successful response. Then send an Anthropic-format request to `/v1/messages` for the same model and verify translated response.

### Implementation for User Story 1 + 3

- [X] T011 [US1] Implement Copilot provider client (NewClient, Name, AuthMode, APIFormat returning "openai") in internal/provider/copilot/client.go — reads stored GitHub token, manages in-memory Copilot session token cache with auto-refresh
- [X] T012 [US1] Implement SendRequest in internal/provider/copilot/client.go — inject editor-simulation headers (Editor-Version, Copilot-Integration-Id, Editor-Plugin-Version, User-Agent), set Authorization bearer token, forward to Copilot API chat/completions endpoint, handle both streaming and non-streaming responses
- [X] T013 [US1] Add `github_copilot` provider case in cmd/serve.go runServe() — instantiate copilot.NewClient with TokenDir from config
- [X] T014 [US1] Update OpenAI handler for format-aware routing in internal/proxy/openai_handler.go — when provider.APIFormat() == "openai", skip OpenAI→Anthropic→OpenAI double-translation and forward request directly to provider
- [X] T015 [US1] Update Anthropic handler for format-aware routing in internal/proxy/handler.go — when provider.APIFormat() == "openai", translate Anthropic→OpenAI before sending, then translate OpenAI→Anthropic on response
- [X] T016 [US1] Handle missing-token error in Copilot provider — when no GitHub token exists, return clear error instructing operator to run `llm-proxy auth copilot`

**Checkpoint**: Both OpenAI-format and Anthropic-format clients can successfully proxy through Copilot. Headers injected automatically. Missing tokens produce helpful error.

---

## Phase 5: User Story 4 - Token Usage Tracking and Cost Logging (Priority: P2)

**Goal**: Copilot requests appear in proxy logs with accurate token counts and cost estimates

**Independent Test**: Send a request through Copilot provider, check web UI logs page shows the request with input/output token counts and estimated cost

### Implementation for User Story 4

- [X] T017 [US4] Extract token usage from non-streaming OpenAI responses in internal/provider/copilot/client.go — parse usage.prompt_tokens and usage.completion_tokens from ChatCompletionResponse, populate provider.Response token fields
- [X] T018 [US4] Handle OpenAI SSE streaming token extraction in OpenAI handler format-aware path in internal/proxy/openai_handler.go — extract usage from final streaming chunk that contains usage data, or from stream events
- [X] T019 [P] [US4] Add default Copilot model pricing entries in internal/pricing/defaults.go — add entries for common Copilot models (claude-opus-4.6, claude-sonnet-4.6, gpt-5.2, etc.) with $0 pricing since Copilot is subscription-based

**Checkpoint**: Copilot requests logged with token counts. Cost tracking works (shows $0 for subscription models unless custom pricing configured).

---

## Phase 6: User Story 5 - Configuration via YAML (Priority: P3)

**Goal**: Operator can configure Copilot provider entirely through config.yaml with minimal settings

**Independent Test**: Create config.yaml with `type: github_copilot` provider and `gc/*` wildcard mapping. Start proxy and verify it initializes correctly and routes `gc/` model requests to Copilot.

### Implementation for User Story 5

- [X] T020 [US5] Validate token directory exists and is writable at provider initialization in internal/provider/copilot/client.go — fail with clear error if directory missing and no stored tokens
- [X] T021 [US5] Apply TokenDir default (`~/.config/llm-proxy/copilot/`) in config loading in internal/config/config.go — expand ~ prefix, set default when empty for github_copilot providers

**Checkpoint**: Full YAML-driven configuration works. Proxy starts and routes Copilot requests with minimal config.

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Security validation, documentation, and final quality checks

- [X] T022 Run `gosec ./...` and `govulncheck ./...` to verify zero findings
- [X] T023 Run `golangci-lint run` to verify zero warnings
- [ ] T024 Validate quickstart.md end-to-end with a real Copilot subscription
- [X] T025 Verify token files are never logged by checking redaction logic in internal/proxy/logger.go handles Copilot auth headers

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — can start immediately
- **Foundational (Phase 2)**: Depends on Phase 1 — BLOCKS all user stories
- **US2 Auth CLI (Phase 3)**: Depends on Phase 2 (needs types from T005)
- **US1+US3 Core Proxy (Phase 4)**: Depends on Phase 2 (needs APIFormat) AND Phase 3 (needs token persistence from T006)
- **US4 Usage Tracking (Phase 5)**: Depends on Phase 4 (needs working provider)
- **US5 Configuration (Phase 6)**: Depends on Phase 4 (needs working provider)
- **Polish (Phase 7)**: Depends on all previous phases

### User Story Dependencies

- **US2 (Auth CLI)**: Can start after Phase 2 — no dependency on other stories
- **US1+US3 (Core Proxy + Headers)**: Depends on US2's token persistence (T006) but NOT on the CLI command itself
- **US4 (Usage Tracking)**: Depends on US1 — extends the provider response handling
- **US5 (Configuration)**: Depends on US1 — extends the config/startup path

### Within Each User Story

- Types/models before services/logic
- Token persistence before OAuth flow
- OAuth flow before CLI command
- Provider client before handler routing
- Core functionality before error handling

### Parallel Opportunities

- T004 and T005 can run in parallel (different files, Phase 2)
- T002 and T003 are sequential (same interface, then implementation)
- T007 and T008 are sequential (device flow before token exchange)
- T009 and T010 are sequential (parent command before subcommand)
- T011 and T012 are sequential (client struct before SendRequest method)
- T014 and T015 can run in parallel (different handler files)
- T017 and T019 can run in parallel (different files)
- T022 and T023 can run in parallel (different lint tools)

---

## Parallel Example: Phase 2 (Foundational)

```bash
# Sequential (interface then implementation):
Task T002: "Add APIFormat() to Provider interface"
Task T003: "Add APIFormat() to Anthropic client"

# Parallel with above (different files):
Task T004: "Add TokenDir to ProviderConfig"
Task T005: "Define Copilot types"
```

## Parallel Example: Phase 4 (US1 Handler Updates)

```bash
# After T013 (serve.go wiring), these can run in parallel:
Task T014: "Update OpenAI handler for format-aware routing"
Task T015: "Update Anthropic handler for format-aware routing"
```

---

## Implementation Strategy

### MVP First (User Stories 1 + 2 Only)

1. Complete Phase 1: Setup
2. Complete Phase 2: Foundational (CRITICAL — blocks all stories)
3. Complete Phase 3: US2 — Auth CLI (`llm-proxy auth copilot` works)
4. Complete Phase 4: US1+US3 — Core proxy with headers
5. **STOP and VALIDATE**: Authenticate with Copilot, send a request through the proxy, verify response
6. Deploy/demo if ready

### Incremental Delivery

1. Setup + Foundational → Foundation ready
2. US2 (Auth CLI) → Can authenticate → Partial demo
3. US1+US3 (Core Proxy) → Full proxy works → MVP!
4. US4 (Usage Tracking) → Logs show token counts → Operational visibility
5. US5 (Configuration) → Clean YAML config → Production-ready
6. Polish → Security validated → Release-ready

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- US1 and US3 are combined in Phase 4 since header injection is integral to SendRequest
- US4 builds on US1's provider — adds response parsing, not a separate code path
- The Copilot session token auto-refresh happens in the provider client, not the auth CLI
- No database schema changes needed — uses existing request_logs table
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
