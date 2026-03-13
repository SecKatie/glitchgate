# Tasks: Models Page

**Input**: Design documents from `/specs/011-models-page/`
**Prerequisites**: plan.md ✅, spec.md ✅, research.md ✅, data-model.md ✅, contracts/web-routes.md ✅, quickstart.md ✅

**Organization**: Tasks are grouped by user story and further divided into focused commit groups. Each commit group is independently compilable, passes all tests, and has a clear, single purpose.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1, US2, US3)
- Exact file paths are included in all task descriptions

---

## Phase 1: Setup — Store Layer

**Purpose**: Add the new store query. Interface addition and implementation must land in the same commit — Go will not compile if an interface method is declared but not implemented.

### Commit A — `feat(store): add ModelUsageSummary type and GetModelUsageSummary`

- [X] T001 Add `ModelUsageSummary` struct (`RequestCount int64`, `InputTokens int64`, `OutputTokens int64`, `TotalCostUSD float64`) to `internal/store/store.go`
- [X] T002 Add `GetModelUsageSummary(ctx context.Context, modelName string) (*ModelUsageSummary, error)` to the `Store` interface in `internal/store/store.go`
- [X] T003 Implement `GetModelUsageSummary` in `internal/store/sqlite.go`: single SQL query `SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(estimated_cost_usd),0) FROM request_logs WHERE model_requested = ?`; return zero-value struct (not error) when no rows exist
- [X] T004 [P] Add table-driven unit tests for `GetModelUsageSummary` in `internal/store/sqlite_test.go`: seed logs for a specific model, assert aggregated count/tokens/cost; assert zero-value result for a model with no log entries

> **✅ Commit A validates**: store interface compiles, `go test ./internal/store/...` passes

---

## Phase 2: Setup — Handler Wiring

**Purpose**: Thread model config into the web handler. Changing `NewHandlers`'s signature and updating the call site must be one commit — Go will not compile if the call in `serve.go` has the wrong arity.

### Commit B — `feat(web): expose model config through Handlers`

- [X] T005 Add `modelList []config.ModelMapping` and `providers []config.ProviderConfig` fields to the `Handlers` struct; add both as trailing parameters to `NewHandlers` in `internal/web/handlers.go`
- [X] T006 Update the `web.NewHandlers(...)` call in `cmd/serve.go` to pass `cfg.ModelList` and `cfg.Providers` as the two new trailing arguments

> **✅ Commit B validates**: binary compiles, `go build ./...` passes

---

## Phase 3: User Story 1 — Browse Configured Models (Priority: P1) 🎯 MVP

**Goal**: `GET /ui/models` renders all `model_list` entries with provider and pricing summary.

**Independent Test**: Navigate to `/ui/models` after login; every config model appears as a table row with model name (linked), provider or "Virtual", and input/output pricing rates or "—".

### Commit C — `feat(web/models): add model list builder with unit tests`

*Pure Go business logic — no HTTP handlers, no templates. Tests run in isolation.*

- [X] T007 [P] [US1] Add `ModelListItem` struct (ModelName, ProviderName, ProviderType, IsVirtual, IsWildcard, Fallbacks `[]string`, Pricing `*pricing.Entry`, HasPricing `bool`, EncodedName `string`) to `internal/web/model_handlers.go`
- [X] T008 [P] [US1] Implement `buildModelList(modelList []config.ModelMapping, providers []config.ProviderConfig, calc *pricing.Calculator) []ModelListItem` in `internal/web/model_handlers.go`: iterate models; resolve provider by name; call `calc.Lookup(providerName, upstreamModel)` for pricing; set IsVirtual when `len(m.Fallbacks) > 0`; set IsWildcard when `strings.HasSuffix(m.ModelName, "/*")`; set `EncodedName = url.PathEscape(m.ModelName)`
- [X] T009 [P] [US1] Write table-driven unit tests for `buildModelList` in `internal/web/model_handlers_test.go` covering: direct model with known pricing, direct model with unknown pricing (HasPricing=false), virtual model with fallbacks, wildcard entry (`gc/*`), model with `Metadata` pricing override reflected in Lookup result; assert EncodedName is correct for a model name containing a slash

> **✅ Commit C validates**: `go test ./internal/web/... -run TestBuildModelList` passes; pure function, zero HTTP or template dependencies

---

### Commit D — `feat(web/models): add Models list page, route, and nav entry`

*Handler + template + route must land together: template must exist for the handler to render, and the nav entry completes the UI.*

- [X] T010 [US1] Implement `(h *Handlers) ModelsPage(w http.ResponseWriter, r *http.Request)` in `internal/web/model_handlers.go`: call `buildModelList(h.modelList, h.providers, h.calc)`; render `models.html` with `map[string]any{"ActiveTab": "models", "Models": items}`
- [X] T011 [P] [US1] Create `internal/web/templates/models.html`: `{{template "layout.html" .}}`; define `title` block as "Models - glitchgate"; define `content` block with a Pico CSS `<table>` iterating `{{range .Models}}` — columns: Model Name (`<a href="/ui/models/{{.EncodedName}}">{{.ModelName}}</a>`), Provider (`{{if .IsVirtual}}Virtual{{else}}{{.ProviderName}} ({{.ProviderType}}){{end}}`), Input ($/M), Output ($/M); fallback "—" when `{{not .HasPricing}}`; empty-state `<p>` when `{{not .Models}}`
- [X] T012 [P] [US1] Write handler tests for `ModelsPage` in `internal/web/model_handlers_test.go` using `httptest.NewRecorder`: verify `200 OK` for a valid config; verify table rows contain expected model names; verify "Virtual" label for a fallback model; verify "—" when pricing is unknown
- [X] T013 [US1] Register `GET /ui/models` route in `cmd/serve.go`, wired to `webHandlers.ModelsPage` (register before the wildcard detail route)
- [X] T014 [US1] Add `<li><a href="/ui/models"{{if eq .ActiveTab "models"}} aria-current="page" class="nav-active"{{end}}>Models</a></li>` after the Costs `<li>` in `internal/web/templates/layout.html`

> **✅ Commit D validates**: `go test ./internal/web/...` passes; navigate to `/ui/models` and see the model list

---

## Phase 4: User Story 2 — View Model Detail (Priority: P2)

**Goal**: Clicking a model opens a detail page showing full pricing, cumulative usage stats, provider info, and an example `curl` command.

**Independent Test**: Click any model on the list; detail page renders pricing tiers (or "—"), usage totals (zeroes for unused models), provider name+type, valid `curl` example with placeholder key, and fallback chain for virtual models.

### Commit E — `feat(web/models): add ModelDetailPage handler with tests`

*Handler + template + route land together for the same reason as Commit D. Store test is parallel.*

- [X] T015 [P] [US2] Add `ModelDetailView` struct (ActiveTab, ModelName, ProviderName, ProviderType, UpstreamModel, IsVirtual, IsWildcard, Fallbacks `[]string`, Pricing `*pricing.Entry`, HasPricing `bool`, Usage `*store.ModelUsageSummary`, CurlExample `string`) to `internal/web/model_handlers.go`
- [X] T016 [US2] Implement `(h *Handlers) ModelDetailPage(w http.ResponseWriter, r *http.Request)` in `internal/web/model_handlers.go`: decode model name via `chi.URLParam(r, "*")` + `url.PathUnescape`; find the model in `h.modelList` (linear scan by `ModelName`); return 404 if not found; resolve provider + pricing via `calc.Lookup`; call `h.store.GetModelUsageSummary`; build `CurlExample` as a multiline string with `model_name` interpolated and `YOUR_PROXY_KEY` placeholder; render `model_detail.html`
- [X] T017 [P] [US2] Create `internal/web/templates/model_detail.html`: `{{template "layout.html" .}}`; `title` block: `{{.ModelName}} — Models - glitchgate`; `content` block with sections: **Pricing** (4-row table: Input, Output, Cache Write, Cache Read in $/M, "—" when `{{not .HasPricing}}`), **Usage** (4 stats: requests, input tokens, output tokens, total cost; zeroes shown, not "N/A"), **Provider** (name + type; "Virtual — fallback chain" when `.IsVirtual`), **Fallback Chain** (`{{if .IsVirtual}}` list of `{{range .Fallbacks}}`), **Example Request** (`<pre><code>{{.CurlExample}}</code></pre>` + note about `/v1/chat/completions`)
- [X] T018 [P] [US2] Write table-driven handler tests for `ModelDetailPage` in `internal/web/model_handlers_test.go`: assert 200 for known direct model; assert 200 for virtual model and fallbacks rendered; assert 404 for unknown model name; assert `YOUR_PROXY_KEY` placeholder appears in response body; use `httptest.NewRecorder` with a stub store implementing `GetModelUsageSummary`
- [X] T019 [US2] Register `GET /ui/models/*` wildcard route in `cmd/serve.go`, wired to `webHandlers.ModelDetailPage` (must be after the exact `/ui/models` route)

> **✅ Commit E validates**: `go test ./internal/web/...` passes; navigate to a model detail page and see all sections

---

## Phase 5: User Story 3 — Navigation (Priority: P3)

**Goal**: Fluid navigation between list and detail via in-page links; browser back works.

**Independent Test**: From the list, click a model with a slash in its name (e.g., `gc/claude-sonnet`) — detail page opens. Back link returns to list.

### Commit F — `feat(web/models): add list↔detail navigation links`

*Small and focused — two template edits, easy to verify visually and via string assertions.*

- [X] T020 [P] [US3] Verify that `internal/web/templates/models.html` model name links use `{{.EncodedName}}` in `href="/ui/models/{{.EncodedName}}"` — update if Commit D used a raw unescaped name; confirm that a model name like `gc/claude-sonnet` produces the href `/ui/models/gc%2Fclaude-sonnet`
- [X] T021 [P] [US3] Add `<p><a href="/ui/models">← Back to Models</a></p>` near the top of the `content` block in `internal/web/templates/model_detail.html`, before the first section heading

> **✅ Commit F validates**: clicking `gc/claude-sonnet` on the list opens `/ui/models/gc%2Fclaude-sonnet`; back link returns to `/ui/models`

---

## Phase 6: Polish & Cross-Cutting Concerns

### Commit G — `chore(models): lint, test, and audit pass`

- [X] T022 [P] Run `make lint` and fix all `golangci-lint` findings in `internal/web/model_handlers.go`, `internal/store/sqlite.go`, and `cmd/serve.go`
- [X] T023 [P] Run `make test` (`go test -race ./...`) and confirm zero failures and zero race conditions across all packages
- [X] T024 Run `make audit` (`gosec` + `govulncheck`) and confirm zero new findings; document any intentional suppressions with inline justification comments
- [ ] T025 Manually verify quickstart.md scenarios: start glitchgate with direct, virtual, and wildcard models configured; confirm list page shows all models; confirm detail page shows overridden pricing where metadata is set; confirm zero-usage stats for a freshly added model; confirm curl example is syntactically valid

> **✅ Commit G validates**: CI green; feature ready for review

---

## Commit Map Summary

| Commit | Scope | Tasks | Why Together |
|--------|-------|-------|-------------|
| A | Store layer | T001–T004 | Interface + impl must compile together; test validates both |
| B | Handler wiring | T005–T006 | Signature change + call site must compile together |
| C | US1 business logic | T007–T009 | Pure functions; fully testable without HTTP or templates |
| D | US1 full page | T010–T014 | Handler + template + route + nav; page is only testable with all four |
| E | US2 full page | T015–T019 | Handler + template + route + store test; page testable with all four |
| F | US3 navigation | T020–T021 | Isolated template edits; verifiable visually and via string assertions |
| G | Polish | T022–T025 | CI hygiene; run after all feature work is complete |

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (Commit A)**: No dependencies — start here
- **Phase 2 (Commit B)**: No dependencies — can run in parallel with Commit A
- **Commit C**: Depends on Commit B (needs `Handlers` with `modelList` and `providers` in scope for type alignment)
- **Commit D**: Depends on Commits B and C (handler needs `buildModelList`; route needs the handler)
- **Commit E**: Depends on Commits A and B (handler calls `h.store.GetModelUsageSummary` and uses `h.modelList`); can start in parallel with Commit C
- **Commit F**: Depends on Commits D and E (both templates must exist)
- **Commit G**: Depends on all prior commits

### Parallel Opportunities

- Commits A and B can be worked simultaneously (store vs. handler layer — different files)
- Within Commit A: T003 and T004 are parallel (implementation vs. test — different files)
- Commits C and the first half of E can be worked in parallel once B lands
- Within Commit D: T011 and T012 are parallel (template vs. handler test — different files)
- Within Commit E: T015, T017, T018 are all parallel (different files — type, template, test)
- T022 and T023 in Commit G are parallel (lint vs. test runs)

---

## Implementation Strategy

### MVP (Commits A → D only)

1. Commit A: Store query
2. Commit B: Handler wiring
3. Commit C: `buildModelList` helper + tests
4. Commit D: Full list page, route, nav
5. **Stop and validate**: `/ui/models` renders; `go test ./...` passes

### Full Feature (All Commits)

1. Commits A + B (parallel)
2. Commit C
3. Commit D → Validate US1
4. Commit E → Validate US2
5. Commit F → Validate US3
6. Commit G → CI green

---

## Notes

- [P] tasks operate on different files — safe to parallelize within a commit
- Commits A and B are Go compile-safety boundaries — do not split interface/implementation or signature/callsite across commits
- The chi catch-all route (`/ui/models/*`) **must** be registered after `GET /ui/models` — see Commit E, T019
- `EncodedName` is pre-computed in `buildModelList` so templates never need a custom `pathEscape` func
- Virtual models have no direct pricing — show "—" for all four pricing cells
- Wildcard models show actual usage totals if requests used the wildcard prefix (e.g., `gc/claude-sonnet` logs against that name, not `gc/*`)
