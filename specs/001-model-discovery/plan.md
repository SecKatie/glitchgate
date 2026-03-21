# Implementation Plan: Automatic Model Discovery

**Branch**: `001-model-discovery` | **Date**: 2026-03-20 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/001-model-discovery/spec.md`

## Summary

Add automatic model discovery so providers with `discover_models: true` auto-populate `resolvedChains` at startup. Each supported provider type (`anthropic`, `openai`, `openai_responses`, `gemini` — including `auth_mode: "vertex"`) calls its model listing endpoint, applies optional `discover_filter` glob patterns, and injects discovered models with `{model_prefix}{upstream_model}` naming. Explicit `model_list` entries always take precedence. Unsupported providers (e.g., `github_copilot`) fail config validation. Discovery API failures degrade gracefully (log warning, start without those models).

## Technical Context

**Language/Version**: Go (latest stable, currently 1.24)
**Primary Dependencies**: `cobra`+`viper` (CLI/config), `chi/v5` (router), `golang.org/x/oauth2` (Vertex auth)
**Storage**: SQLite via `modernc.org/sqlite` — not impacted by this feature
**Testing**: `go test -race ./...` with `testify/require`, table-driven tests
**Target Platform**: Linux server (single statically-linked binary, CGO_ENABLED=0)
**Project Type**: CLI / web-service (LLM API reverse proxy)
**Performance Goals**: Discovery adds ≤2s to startup time (SC-006)
**Constraints**: No new external dependencies; provider listing uses existing `http.Client` from `provider.BuildHTTPClient()`
**Scale/Scope**: Typically 2–5 configured providers, each returning 10–50 models

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Speed Above All | PASS | Discovery is startup-only, not on hot path. No per-request overhead. |
| II. Efficient Use of Resources | PASS | Single HTTP call per provider at startup. No goroutine-per-token. No new dependencies. |
| III. Clean Abstractions | PASS | New `ModelDiscoverer` interface on provider package. Adding discovery does not modify existing provider implementations' `SendRequest` logic. |
| IV. Correctness and Compatibility | PASS | Explicit `model_list` precedence preserved. Backward compatible (disabled by default). |
| V. Security by Default | PASS | Listing endpoints use same TLS and auth as existing provider clients. No new secret handling. |

## Project Structure

### Documentation (this feature)

```text
specs/001-model-discovery/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output (N/A — no new external API)
└── tasks.md             # Phase 2 output (/speckit.tasks command)
```

### Source Code (repository root)

```text
internal/
├── config/
│   └── config.go              # Add DiscoverModels, ModelPrefix, DiscoverFilter to ProviderConfig
│                               # Add discoverModels() called from Load() before buildResolvedChains()
├── provider/
│   ├── provider.go            # Add ModelDiscoverer interface
│   ├── anthropic/
│   │   └── client.go          # Implement ListModels (direct + vertex)
│   ├── openai/
│   │   └── client.go          # Implement ListModels (direct only)
│   ├── gemini/
│   │   └── client.go          # Implement ListModels (api_key + vertex)
│   └── copilot/
│       └── client.go          # No change (does not implement ModelDiscoverer)
└── app/
    └── providers.go           # Wire discovery into provider factory
```

**Structure Decision**: Extends existing package boundaries. No new packages needed — discovery logic integrates into `config/` (orchestration) and each provider package (API calls).

## Complexity Tracking

No constitution violations. Table not required.
