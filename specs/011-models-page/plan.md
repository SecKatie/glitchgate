# Implementation Plan: Models Page

**Branch**: `011-models-page` | **Date**: 2026-03-13 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/011-models-page/spec.md`

## Summary

Add a Models page to the web UI that lists all models from the `model_list` config with pricing summaries, and a detail page showing full pricing, cumulative usage statistics, provider info, and an example `curl` command. No schema changes required — usage data is aggregated from the existing `request_logs` table. A new store query (`GetModelUsageSummary`) will aggregate per-model stats. Config access will be added to `Handlers` so the model list and provider details are available at render time.

## Technical Context

**Language/Version**: Go 1.26.1
**Primary Dependencies**: chi/v5 (router), HTMX 2.0.4 (CDN), Pico CSS v2 (CDN), internal/pricing, internal/config, internal/store
**Storage**: SQLite (read-only for this feature; no schema changes)
**Testing**: `go test -race ./...` with testify/require; table-driven tests
**Target Platform**: Linux server (single binary)
**Project Type**: Web service (proxy + admin UI)
**Performance Goals**: Page renders in <200ms under normal load (non-hot-path UI)
**Constraints**: Config is read at startup and passed to handlers; no runtime config reload needed
**Scale/Scope**: Single page + detail page; ~N models where N is the number of entries in model_list

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Speed Above All | ✅ Pass | Models page is UI-only, not on the proxy hot path. No performance concern. |
| II. Efficient Use of Resources | ✅ Pass | One new SQL query (aggregation over request_logs); results fetched per page load. No caching needed at this scale. Config is already in memory. |
| III. Clean Abstractions | ✅ Pass | New `GetModelUsageSummary` store method follows existing store interface pattern. Handler logic isolated in `internal/web/model_handlers.go`. Config access added as a field on `Handlers` — no new package dependencies. |
| IV. Correctness and Compatibility | ✅ Pass | Read-only feature; no translation or proxying involved. |
| V. Security by Default | ✅ Pass | Page is behind existing session auth middleware. No secrets exposed — `curl` example uses a hardcoded placeholder key. Config values (API keys) are never rendered. |

No violations. No complexity tracking required.

## Project Structure

### Documentation (this feature)

```text
specs/011-models-page/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   └── web-routes.md
└── tasks.md             # Phase 2 output (/speckit.tasks command)
```

### Source Code (repository root)

```text
internal/
├── web/
│   ├── model_handlers.go          # new: ModelsPage, ModelDetailPage handlers
│   ├── model_handlers_test.go     # new: table-driven tests
│   └── templates/
│       ├── models.html            # new: model list page
│       ├── model_detail.html      # new: model detail page
│       └── layout.html            # edit: add "Models" nav entry
├── store/
│   ├── store.go                   # edit: add GetModelUsageSummary to Store interface
│   └── sqlite.go                  # edit: implement GetModelUsageSummary
└── web/
    └── handlers.go                # edit: add cfg fields to Handlers, NewHandlers signature
cmd/
└── serve.go                       # edit: pass config.ModelList + config.Providers to NewHandlers
```

**Structure Decision**: Single project, existing layout. All changes are additions or targeted edits to existing files.

## Complexity Tracking

> No violations to justify.
