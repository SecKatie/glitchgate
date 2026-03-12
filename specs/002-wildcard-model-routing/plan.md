# Implementation Plan: Wildcard Model Routing

**Branch**: `002-wildcard-model-routing` | **Date**: 2026-03-11 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/002-wildcard-model-routing/spec.md`

## Summary

Add wildcard prefix matching to the model routing system so that a single `model_list` entry like `claude_max/*` can route any model prefixed with `claude_max/` to a designated provider, stripping the prefix and using the remainder as the upstream model name. This eliminates the need to enumerate every model individually per provider. The change is localized to `config.FindModel()` — both the Anthropic and OpenAI handlers already consume its output, so wildcard support flows through automatically.

## Technical Context

**Language/Version**: Go 1.24+
**Primary Dependencies**: cobra + viper (CLI/config), chi/v5 (HTTP router)
**Storage**: SQLite via modernc.org/sqlite (no change needed — logs already store model_requested and model_upstream as strings)
**Testing**: `go test -race ./...` with `testify/require`
**Target Platform**: Linux/macOS server (single static binary, CGO_ENABLED=0)
**Project Type**: CLI / web service (proxy)
**Performance Goals**: Negligible latency added — string prefix comparison on a small config list
**Constraints**: No new dependencies. No database schema changes. Backward compatible with existing exact-match configs.
**Scale/Scope**: Typically <20 model_list entries; linear scan is fine.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Speed Above All | PASS | Wildcard matching is a string prefix check on a small slice — zero-allocation, O(n) where n is model_list size (typically <20). No hot-path impact. |
| II. Efficient Use of Resources | PASS | No new dependencies, no new goroutines, no allocations beyond a single `ModelMapping` struct return. |
| III. Clean Abstractions | PASS | Change is contained in `config.FindModel()`. Handlers and providers are untouched. Provider interface boundary preserved. |
| IV. Correctness and Compatibility | PASS | Exact matches take priority (scanned first). Existing behavior is unchanged for non-wildcard configs. Contract tests will verify. |
| V. Security by Default | PASS | No new auth surface. Wildcard routing still requires proxy key authentication. No secrets exposed. |

No violations. No complexity tracking needed.

## Project Structure

### Documentation (this feature)

```text
specs/002-wildcard-model-routing/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
└── tasks.md             # Phase 2 output (created by /speckit.tasks)
```

### Source Code (repository root)

```text
internal/
├── config/
│   ├── config.go          # FindModel() updated with wildcard logic
│   └── config_test.go     # New wildcard matching tests
```

No new files, packages, or directories needed. No contracts/ directory because this feature changes internal routing logic only — no external API contract changes (clients already send arbitrary model strings; the proxy just matches them differently now).

**Structure Decision**: All changes fit within the existing `internal/config/` package. `FindModel()` is the single point of change, and both proxy handlers (`handler.go`, `openai_handler.go`) consume its output without modification.
