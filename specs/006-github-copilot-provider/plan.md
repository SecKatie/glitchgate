# Implementation Plan: GitHub Copilot Provider

**Branch**: `006-github-copilot-provider` | **Date**: 2026-03-11 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/006-github-copilot-provider/spec.md`

## Summary

Add a `github_copilot` provider type to llm-proxy that authenticates via GitHub's OAuth device flow and forwards requests to the Copilot API using OpenAI chat completions format. Includes a standalone `llm-proxy auth copilot` CLI command for authentication, automatic editor-simulation header injection, format-aware request routing (both OpenAI and Anthropic clients can use Copilot models), and token usage tracking.

## Technical Context

**Language/Version**: Go 1.24+
**Primary Dependencies**: cobra/viper (CLI), chi/v5 (router), net/http (OAuth + Copilot API calls), encoding/json
**Storage**: Filesystem (JSON token files with 0600 permissions); existing SQLite for request logging (no schema changes)
**Testing**: `go test -race ./...` with `testify/require`, table-driven tests
**Target Platform**: Linux/macOS server (single static binary, CGO_ENABLED=0)
**Project Type**: CLI + web service
**Performance Goals**: < 500ms additional latency over direct Copilot access
**Constraints**: No CGO, single binary, token files at 0600/directory at 0700
**Scale/Scope**: Same as existing proxy — single modest VM

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
| --------- | ------ | ----- |
| I. Speed Above All | PASS | Copilot provider uses `net/http.Client` directly (same as Anthropic). Format-aware routing eliminates unnecessary double-translation for OpenAI clients. Copilot session token cached in memory with expiry check. |
| II. Efficient Use of Resources | PASS | No new goroutines beyond existing patterns. Token refresh is synchronous on request path only when expired. No new dependencies (uses stdlib `net/http` for OAuth + API calls). Single static binary preserved. |
| III. Clean Abstractions | PASS | New provider implements existing `Provider` interface + new `APIFormat()` method. Provider-specific types stay in `internal/provider/copilot/`. Token management is internal to the provider package. Adding `APIFormat()` to interface does not modify existing Anthropic implementation (just adds one method). |
| IV. Correctness and Compatibility | PASS | Uses existing `internal/translate` functions for format conversion. Streaming SSE forwarded incrementally. Both OpenAI and Anthropic client formats supported via format-aware handler routing. Contract tests required for Copilot request/response translation. |
| V. Security by Default | PASS | All connections over TLS. Token files at 0600, directory at 0700. GitHub OAuth token never logged. Copilot session token never exposed in logs. `gosec`/`govulncheck` must pass. |

**Post-Phase 1 Re-check**: All gates still pass. The `APIFormat()` interface method is the main architectural change; it's additive and doesn't break existing code.

## Project Structure

### Documentation (this feature)

```text
specs/006-github-copilot-provider/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   └── provider-interface.md
└── tasks.md             # Phase 2 output (from /speckit.tasks)
```

### Source Code (repository root)

```text
cmd/
├── root.go              # existing
├── serve.go             # MODIFY: add github_copilot provider case
└── auth.go              # NEW: `llm-proxy auth` parent command
    └── auth_copilot.go  # NEW: `llm-proxy auth copilot` subcommand

internal/
├── provider/
│   ├── provider.go      # MODIFY: add APIFormat() to interface
│   ├── anthropic/
│   │   └── client.go    # MODIFY: add APIFormat() returning "anthropic"
│   └── copilot/         # NEW: entire package
│       ├── client.go    # Provider implementation (SendRequest, headers, auth)
│       ├── oauth.go     # Device flow + token exchange logic
│       ├── token.go     # Token persistence (read/write JSON, file permissions)
│       └── types.go     # Copilot-specific types (tokens, device flow response)
├── proxy/
│   ├── handler.go       # MODIFY: format-aware routing for Anthropic handler
│   └── openai_handler.go # MODIFY: format-aware routing for OpenAI handler
└── translate/           # NO CHANGES: existing functions reused
```

**Structure Decision**: Follows existing single-project layout. New `copilot` package under `internal/provider/` mirrors the `anthropic` package structure. CLI auth command added as new cobra subcommand.

## Complexity Tracking

No constitution violations to justify.
