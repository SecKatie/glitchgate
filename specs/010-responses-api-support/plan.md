# Implementation Plan: OpenAI Responses API Support

**Branch**: `010-responses-api-support` | **Date**: 2026-03-12 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/010-responses-api-support/spec.md`
**Supersedes**: Spec 009 (OpenAI Upstream Provider) — never implemented

## Summary

Add full OpenAI provider support (Chat Completions + Responses API) and make the Responses API a first-class format in the proxy's 3×3 translation matrix. This introduces:

1. **OpenAI provider** (`internal/provider/openai/`) targeting any OpenAI-compatible endpoint with configurable upstream format (Chat Completions or Responses API)
2. **Responses API handler** (`/v1/responses` endpoint) as a new client-facing input format
3. **6 new translation paths** completing the Anthropic ↔ Chat Completions ↔ Responses API matrix
4. **Responses API type definitions** and streaming event translation
5. **Multimodal content translation** (images, audio, files) across formats

The approach follows existing patterns: pure translation functions, provider interface implementation, format-aware handler dispatch, and streaming relay with token extraction.

## Technical Context

**Language/Version**: Go 1.26.1 (module `github.com/seckatie/glitchgate`)
**Primary Dependencies**: chi/v5 (router), go-resty/v3 (upstream calls), cobra+viper (CLI/config), modernc.org/sqlite (storage), goose/v3 (migrations), testify/require (tests)
**Storage**: SQLite — no schema changes; existing `request_logs` table supports new `source_format` value `"responses"`
**Testing**: `go test -race ./...` with table-driven tests; contract tests against Responses API schema
**Target Platform**: Linux/macOS server, single binary, CGO_ENABLED=0
**Project Type**: Web service (API proxy)
**Performance Goals**: Translation overhead negligible relative to upstream LLM latency; streaming relay without buffering
**Constraints**: Single modest VM (2 vCPU / 2 GB RAM); memory scales linearly with concurrent connections; no goroutine-per-token
**Scale/Scope**: Same as existing proxy; adds ~15 new source files across provider, translate, and proxy packages

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

### I. Speed Above All — PASS
- Translation functions are pure, allocation-minimal transformations on pre-parsed JSON
- Streaming relay forwards SSE events incrementally; never buffers full responses
- Responses API passthrough (same-format) has near-zero overhead
- Benchmark tests required for all translation hot paths per constitution

### II. Efficient Use of Resources — PASS
- No new goroutine-per-token patterns; uses existing goroutine-per-request model
- Single binary, CGO_ENABLED=0 maintained
- go-resty/v3 reused for upstream calls (no new HTTP client dependency)
- No new dependencies beyond what's already in the module

### III. Clean Abstractions — PASS
- New OpenAI provider behind existing `Provider` interface; adding it does NOT modify Anthropic or Copilot providers
- Translation logic is pure functions on internal types (no HTTP framework types)
- Responses API types live in `internal/translate/` alongside existing OpenAI types
- Provider-specific types stay in `internal/provider/openai/`
- `APIFormat()` extended with `"responses"` value; handlers route on this

### IV. Correctness and Compatibility — PASS
- Bidirectional translation preserves content, tool calls, and token usage
- Streaming forwarded incrementally via SSE
- Error codes mapped semantically between formats
- Contract tests required for every translation path against published API schemas

### V. Security by Default — PASS
- All upstream connections use TLS (existing behavior)
- API key handling follows existing proxy_key/forward patterns
- Input validation at `/v1/responses` endpoint before proxying
- No new secret storage; uses existing config mechanisms
- gosec + govulncheck required before release

## Project Structure

### Documentation (this feature)

```text
specs/010-responses-api-support/
├── plan.md              # This file
├── research.md          # Phase 0: API research & design decisions
├── data-model.md        # Phase 1: Type definitions & entity model
├── quickstart.md        # Phase 1: Developer onboarding
├── contracts/           # Phase 1: API contracts
│   ├── responses-api-input.md    # Client-facing /v1/responses contract
│   └── responses-api-upstream.md # Upstream Responses API contract
└── tasks.md             # Phase 2 output (/speckit.tasks command)
```

### Source Code (repository root)

```text
internal/
├── provider/
│   └── openai/              # NEW: OpenAI provider implementation
│       ├── client.go        # Provider interface implementation
│       └── types.go         # Provider-specific config types
├── translate/
│   ├── responses_types.go           # NEW: Responses API type definitions
│   ├── responses_to_anthropic.go    # NEW: Responses → Anthropic request
│   ├── anthropic_to_responses.go    # NEW: Anthropic → Responses request
│   ├── responses_to_openai.go       # NEW: Responses → Chat Completions request
│   ├── openai_to_responses.go       # NEW: Chat Completions → Responses request
│   ├── responses_to_anthropic_response.go  # NEW: Anthropic response → Responses format
│   ├── anthropic_to_responses_response.go  # NEW: Responses response → Anthropic format
│   ├── responses_to_openai_response.go     # NEW: Chat Completions response → Responses format
│   ├── openai_to_responses_response.go     # NEW: Responses response → Chat Completions format
│   ├── responses_stream_translator.go      # NEW: Responses SSE ↔ other format SSE
│   └── translate_test.go           # MODIFIED: Add Responses API test cases
├── proxy/
│   ├── handler.go           # MODIFIED: Add "responses" format routing in fallback chains
│   ├── openai_handler.go    # MODIFIED: Add "responses" upstream routing
│   ├── responses_handler.go # NEW: /v1/responses endpoint handler
│   ├── stream.go            # MODIFIED: Add RelayResponsesSSEStream()
│   └── logger.go            # MODIFIED: Support source_format "responses"
├── config/
│   └── config.go            # MODIFIED: Add "openai" provider type + api_type field
└── pricing/
    └── calculator.go        # MODIFIED: Add OpenAI model pricing support

cmd/
└── serve.go                 # MODIFIED: Register OpenAI provider + /v1/responses route
```

**Structure Decision**: Follows existing project structure conventions. New files mirror existing patterns (e.g., `responses_types.go` alongside `openai_types.go`; `provider/openai/` alongside `provider/anthropic/`). No new top-level packages or structural changes.

## Complexity Tracking

> No constitution violations detected. All design decisions align with existing patterns.
