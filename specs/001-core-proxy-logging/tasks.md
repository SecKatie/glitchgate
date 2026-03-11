# Tasks: Core Proxy with Logging & Cost Monitoring

**Input**: Design documents from `/specs/001-core-proxy-logging/`
**Prerequisites**: plan.md (required), spec.md (required), research.md, data-model.md, contracts/

**Tests**: Not explicitly requested in the spec. Test tasks are not included. Contract tests and benchmarks are included in the Polish phase per constitution requirements.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- Single Go project at repository root
- CLI commands in `cmd/`
- All internal packages in `internal/`
- SQL queries in `queries/`
- Migrations embedded from `internal/store/migrations/`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Project initialization, dependency management, and tooling

- [X] T001 Initialize Go module with `go mod init codeberg.org/kglitchy/llm-proxy` and create directory structure per plan.md
- [X] T002 Create main.go entry point at main.go
- [X] T003 [P] Create Makefile with build, test, lint, and audit targets at Makefile
- [X] T004 [P] Create .golangci.yml with staticcheck, gosec, errcheck, revive, gofumpt at .golangci.yml
- [X] T005 [P] Create .goreleaser.yaml with CGO_ENABLED=0 and AGPL-3.0 license at .goreleaser.yaml
- [X] T006 Add all Go dependencies: cobra, viper, chi/v5, go-resty/v3, modernc.org/sqlite, goose/v3, testify via go get

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core infrastructure that MUST be complete before ANY user story can be implemented

**CRITICAL**: No user story work can begin until this phase is complete

- [X] T007 Define configuration types (providers, model_list, pricing, master_key, listen address) in internal/config/config.go
- [X] T008 Implement config loading with viper (YAML file, env vars, CLI flags) in internal/config/config.go
- [X] T009 [P] Define shared internal types (RequestLog, ModelMapping, ProviderConfig, PricingEntry) in internal/models/types.go
- [X] T010 [P] Define Provider interface (SendRequest, SendStreamingRequest, Name, AuthMode) in internal/provider/provider.go
- [X] T011 Create goose SQL migration 001: proxy_keys table in internal/store/migrations/001_create_proxy_keys.sql
- [X] T012 Create goose SQL migration 002: request_logs table with indexes in internal/store/migrations/002_create_request_logs.sql
- [X] T013 Create sqlc.yaml configuration at sqlc.yaml
- [X] T014 Write sqlc queries for proxy_keys CRUD (create, get_by_hash, list_active, revoke) in queries/proxy_keys.sql
- [X] T015 Write sqlc queries for request_logs insert in queries/request_logs.sql
- [X] T016 Generate sqlc Go code via `sqlc generate`
- [X] T017 Implement Store interface with migration runner and connection management in internal/store/store.go
- [X] T018 Implement SQLite Store using generated sqlc code in internal/store/sqlite.go
- [X] T019 [P] Implement proxy API key generation (llmp_sk_ prefix + 32 hex chars), bcrypt hashing, and verification in internal/auth/keys.go
- [X] T020 [P] Implement pricing calculator (cost from token counts + per-model pricing config) in internal/pricing/calculator.go
- [X] T021 [P] Embed default model pricing for Anthropic models (claude-sonnet-4, claude-opus-4, claude-haiku-4) in internal/pricing/defaults.go

**Checkpoint**: Foundation ready — config, database, store, auth, pricing, and provider interface all in place

---

## Phase 3: User Story 1 — Proxy LLM Requests with Logging (Priority: P1) MVP

**Goal**: Proxy Anthropic Messages API requests to upstream, log all request/response pairs with metadata and cost

**Independent Test**: Point an Anthropic SDK client at the proxy, send a request, verify the response matches upstream, and confirm a log entry exists with correct metadata

### Implementation for User Story 1

- [X] T022 [P] [US1] Define Anthropic request/response types (MessagesRequest, MessagesResponse, SSE event types) in internal/provider/anthropic/types.go
- [X] T023 [US1] Implement Anthropic provider client with net/http for streaming and go-resty for non-streaming in internal/provider/anthropic/client.go
- [X] T024 [US1] Implement dual auth modes in Anthropic client: proxy_key mode (send configured API key) and forward mode (pass client Authorization header) in internal/provider/anthropic/client.go
- [X] T025 [US1] Implement request body redaction (strip API keys and Authorization headers from JSON before logging) in internal/proxy/redact.go
- [X] T026 [US1] Implement proxy handler: parse request, resolve model via config mapping, select provider, dispatch request, log result in internal/proxy/handler.go
- [X] T027 [US1] Implement SSE stream relay with io.TeeReader capture, http.ResponseController flushing, and context-based client disconnect handling in internal/proxy/stream.go
- [X] T028 [US1] Implement async log writer (goroutine + channel) that persists RequestLog entries to store without blocking the proxy path in internal/proxy/logger.go
- [X] T029 [US1] Implement proxy API key auth middleware for chi (validate x-api-key or x-proxy-api-key header, reject with 401 if invalid) in internal/proxy/middleware.go
- [X] T030 [US1] Implement cobra root command with viper config initialization in cmd/root.go
- [X] T031 [US1] Implement cobra serve command: initialize store, run migrations, build chi router with proxy routes (/v1/messages), start HTTP server in cmd/serve.go
- [X] T032 [US1] Implement cobra keys subcommands (create --label, list, revoke) in cmd/keys.go

**Checkpoint**: Proxy accepts Anthropic Messages API requests, forwards to upstream (both auth modes), logs everything with cost. Usable with Claude Code immediately.

---

## Phase 4: User Story 2 — View Request/Response Logs (Priority: P2)

**Goal**: Web UI log viewer with filtering, sorting, pagination, and detail view

**Independent Test**: After proxying several requests, open the web UI, verify all requests appear, click one for details, filter by model, sort by latency

### Implementation for User Story 2

- [X] T033 [P] [US2] Write sqlc queries for request_logs listing with filtering (model, status, key, date range), sorting, and pagination in queries/request_logs.sql
- [X] T034 [US2] Regenerate sqlc Go code and update SQLite store with log listing and detail methods in internal/store/sqlite.go
- [X] T035 [P] [US2] Implement in-memory session store (create, validate, expire, delete) with crypto/rand token generation in internal/auth/session.go
- [X] T036 [P] [US2] Add HTMX (htmx.min.js) and Pico CSS (pico.min.css) to internal/web/static/
- [X] T037 [US2] Create go:embed declarations for templates and static assets in internal/web/embed.go
- [X] T038 [US2] Create base HTML layout template (head, nav with Logs/Costs tabs, content block, footer) in internal/web/templates/layout.html
- [X] T039 [US2] Create login page template (master key input form) in internal/web/templates/login.html
- [X] T040 [US2] Implement session auth middleware for web UI routes (check cookie or Authorization header, redirect to login if invalid) in internal/web/middleware.go
- [X] T041 [US2] Implement login handler (POST /ui/api/login: validate master key, create session, set cookie) and logout handler in internal/web/handlers.go
- [X] T042 [US2] Create log list page template with HTMX-powered filter controls, sortable column headers, and paginated table in internal/web/templates/logs.html
- [X] T043 [US2] Create log list rows fragment template (HTMX partial for table body updates) in internal/web/templates/fragments/log_rows.html
- [X] T044 [US2] Implement logs list handler (GET /ui/logs: render full page; GET /ui/api/logs: return JSON or HTML fragment based on Accept header) in internal/web/handlers.go
- [X] T045 [US2] Create log detail page template (full request/response bodies, metadata) in internal/web/templates/log_detail.html
- [X] T046 [US2] Implement log detail handler (GET /ui/logs/:id and GET /ui/api/logs/:id) in internal/web/handlers.go
- [X] T047 [US2] Wire web UI routes into chi router in cmd/serve.go: mount /ui/ pages and /ui/api/ JSON endpoints with session middleware, serve static assets at /ui/static/

**Checkpoint**: Web UI accessible at /ui/, master key login works, log viewer shows all proxied requests with filtering, sorting, pagination, and detail views

---

## Phase 5: User Story 3 — Monitor Cost Usage (Priority: P3)

**Goal**: Cost dashboard showing total spend, per-model/per-key breakdowns, and cost over time

**Independent Test**: After proxying requests across multiple models, open the cost dashboard, verify totals match expected values, check per-model breakdown and daily cost chart

### Implementation for User Story 3

- [X] T048 [P] [US3] Write sqlc queries for cost aggregation: total cost, group by model, group by key, group by time interval (day/week/month) in queries/costs.sql
- [X] T049 [US3] Regenerate sqlc Go code and update SQLite store with cost summary and timeseries methods in internal/store/sqlite.go
- [X] T050 [US3] Implement cost summary API handler (GET /ui/api/costs: total, breakdown by model/key, filtered by date range and key) in internal/web/handlers.go
- [X] T051 [US3] Implement cost timeseries API handler (GET /ui/api/costs/timeseries: cost per day/week/month) in internal/web/handlers.go
- [X] T052 [US3] Create cost dashboard page template with summary cards (total cost, total requests, total tokens), breakdown table, and time-series chart in internal/web/templates/costs.html
- [X] T053 [US3] Create cost fragments templates (HTMX partials for breakdown table and chart updates) in internal/web/templates/fragments/cost_summary.html
- [X] T054 [US3] Wire cost routes into chi router in cmd/serve.go: mount /ui/costs page and /ui/api/costs endpoints

**Checkpoint**: Cost dashboard shows accurate totals, per-model and per-key breakdowns, and daily/weekly/monthly cost trends. Filterable by API key.

---

## Phase 6: User Story 4 — OpenAI-Compatible Endpoint (Priority: P4)

**Goal**: Accept OpenAI Chat Completions API requests, translate to/from Anthropic format, proxy through existing infrastructure

**Independent Test**: Point an OpenAI SDK client at the proxy, send a chat completion request, verify response conforms to OpenAI schema, confirm log entry has correct metadata

### Implementation for User Story 4

- [X] T055 [P] [US4] Define OpenAI Chat Completions request/response types and streaming chunk types in internal/translate/openai_types.go
- [X] T056 [P] [US4] Implement OpenAI → Anthropic request translation (system message extraction, field mapping, tool format adaptation) in internal/translate/openai_to_anthropic.go
- [X] T057 [P] [US4] Implement Anthropic → OpenAI non-streaming response translation (stop_reason mapping, usage field mapping, response structure) in internal/translate/anthropic_to_openai.go
- [X] T058 [US4] Implement Anthropic SSE → OpenAI SSE streaming translation (event-by-event translation: message_start → role chunk, content_block_delta → content chunk, message_delta → finish chunk, message_stop → [DONE]) in internal/translate/stream_translator.go
- [X] T059 [US4] Implement OpenAI proxy handler: parse OpenAI request, translate to Anthropic, dispatch via existing proxy infrastructure, translate response back, return in OpenAI format in internal/proxy/openai_handler.go
- [X] T060 [US4] Wire /v1/chat/completions route into chi router with same auth middleware in cmd/serve.go

**Checkpoint**: OpenAI SDK clients work transparently through the proxy. Requests are logged with source_format="openai" and both formats visible in log detail.

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Hardening, testing, and validation across all stories

- [X] T061 [P] Implement edge case handling: upstream 5xx errors returned to caller with logged error details in internal/proxy/handler.go
- [X] T062 [P] Implement edge case handling: mid-stream disconnect cleanup (log partial response with error status) in internal/proxy/stream.go
- [X] T063 [P] Implement edge case handling: storage failure graceful degradation (proxy continues, warning emitted) in internal/proxy/logger.go
- [X] T064 [P] Implement edge case handling: unknown model pricing shows "unknown" cost in web UI in internal/web/templates/
- [X] T065 [P] Add contract tests for Anthropic Messages API proxy (non-streaming + streaming, both auth modes) in internal/proxy/handler_test.go
- [X] T066 [P] Add contract tests for OpenAI Chat Completions API proxy (non-streaming + streaming, translation correctness) in internal/proxy/openai_handler_test.go
- [X] T067 [P] Add translation unit tests (OpenAI ↔ Anthropic request/response mapping, all stop_reason variants, tool calls) in internal/translate/translate_test.go
- [X] T068 [P] Add benchmarks for proxy handler, SSE stream relay, and translation functions in internal/proxy/benchmark_test.go
- [X] T069 Run gosec and govulncheck, fix any findings
- [X] T070 Run quickstart.md validation: build binary, create config, generate key, start proxy, test with curl
- [X] T071 Add pre-commit hook for golangci-lint, gosec, and govulncheck

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion — BLOCKS all user stories
- **User Story 1 (Phase 3)**: Depends on Foundational — BLOCKS US2/US3 (they need logs to exist)
- **User Story 2 (Phase 4)**: Depends on US1 (logs must exist to be viewed)
- **User Story 3 (Phase 5)**: Depends on US1 (cost data comes from logged requests); can run in parallel with US2
- **User Story 4 (Phase 6)**: Depends on US1 (uses existing proxy infrastructure); can run in parallel with US2/US3
- **Polish (Phase 7)**: Depends on all desired user stories being complete

### User Story Dependencies

- **US1 (P1)**: Can start after Foundational (Phase 2) — No dependencies on other stories
- **US2 (P2)**: Depends on US1 for log data in the database — Web UI infrastructure is independent
- **US3 (P3)**: Depends on US1 for cost data — Can run in parallel with US2 (shares web UI base from US2)
- **US4 (P4)**: Depends on US1 proxy infrastructure — Translation layer is fully independent

### Within Each User Story

- Models/types before services/clients
- Services before handlers
- Handlers before route wiring
- Core implementation before integration

### Parallel Opportunities

- Phase 1: T003, T004, T005 can run in parallel
- Phase 2: T009, T010, T019, T020, T021 can run in parallel (after T007/T008)
- Phase 3: T022 can run in parallel with T023 setup
- Phase 4: T033, T035, T036 can run in parallel
- Phase 5: T048 can start while US2 is in progress
- Phase 6: T055, T056, T057 can all run in parallel
- Phase 7: T061–T068 can all run in parallel

---

## Parallel Example: User Story 1

```bash
# After foundational phase, launch parallel tasks:
Task: "Define Anthropic types in internal/provider/anthropic/types.go"  # T022

# Then sequential chain:
Task: "Implement Anthropic client in internal/provider/anthropic/client.go"  # T023
Task: "Implement dual auth modes in internal/provider/anthropic/client.go"  # T024
Task: "Implement request redaction in internal/proxy/redact.go"  # T025
Task: "Implement proxy handler in internal/proxy/handler.go"  # T026
Task: "Implement SSE stream relay in internal/proxy/stream.go"  # T027
Task: "Implement async log writer in internal/proxy/logger.go"  # T028
```

---

## Parallel Example: User Story 4

```bash
# These three translation tasks can run in parallel:
Task: "Define OpenAI types in internal/translate/openai_types.go"  # T055
Task: "Implement OpenAI→Anthropic translation in internal/translate/openai_to_anthropic.go"  # T056
Task: "Implement Anthropic→OpenAI translation in internal/translate/anthropic_to_openai.go"  # T057

# Then sequential:
Task: "Implement streaming translation in internal/translate/stream_translator.go"  # T058
Task: "Implement OpenAI handler in internal/proxy/openai_handler.go"  # T059
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup
2. Complete Phase 2: Foundational (CRITICAL — blocks all stories)
3. Complete Phase 3: User Story 1
4. **STOP and VALIDATE**: Test with Claude Code + Max subscription
5. You can now proxy and log all LLM usage immediately

### Incremental Delivery

1. Complete Setup + Foundational → Foundation ready
2. Add User Story 1 → Test independently → **MVP! Start using with Claude Code**
3. Add User Story 2 → Test independently → **View your logs in the browser**
4. Add User Story 3 → Test independently → **See your costs**
5. Add User Story 4 → Test independently → **OpenAI SDK apps work too**
6. Each story adds value without breaking previous stories

---

## Notes

- [P] tasks = different files, no dependencies on incomplete tasks
- [Story] label maps task to specific user story for traceability
- Each user story should be independently completable and testable
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- sqlc queries are split across phases: basic CRUD in foundational, listing/filtering in US2, aggregations in US3
- SSE routes MUST be excluded from chi's Compress middleware (per research.md)
- Use net/http.Client for streaming upstream calls, go-resty for non-streaming (per research.md)
