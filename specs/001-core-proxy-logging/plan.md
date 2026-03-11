# Implementation Plan: Core Proxy with Logging & Cost Monitoring

**Branch**: `001-core-proxy-logging` | **Date**: 2026-03-11 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/001-core-proxy-logging/spec.md`

## Summary

Build a Go-based LLM proxy that exposes Anthropic- and OpenAI-compatible
endpoints, forwards requests to configured upstream providers (Anthropic
for MVP), logs all request/response pairs with metadata, calculates
costs from token usage, and serves a basic web UI for viewing logs and
monitoring costs. The proxy supports multiple API keys for usage
segmentation, dual upstream auth modes (proxy-owned key and client
credential forwarding), and configurable model name mapping.

## Technical Context

**Language/Version**: Go 1.24+ (latest stable)
**Primary Dependencies**: cobra + viper (CLI/config), chi/v5 (HTTP
router), net/http (upstream SSE streaming), go-resty/v3 (non-streaming
upstream calls), modernc.org/sqlite (database), goose/v3 (migrations),
sqlc (query generation), testify (testing), HTMX + Pico CSS (embedded
web UI)
**Storage**: SQLite via modernc.org/sqlite (pure Go, no CGO)
**Testing**: `go test -race ./...` with testify/require, table-driven
tests, contract tests for API schemas, benchmarks for hot paths
**Target Platform**: Linux and macOS (single static binary,
CGO_ENABLED=0)
**Project Type**: CLI + web service (single binary)
**Performance Goals**: <50ms proxy overhead for non-streaming requests,
50 concurrent requests without degradation
**Constraints**: 2 vCPU / 2 GB RAM target, CGO_ENABLED=0, AGPL-3.0
**Scale/Scope**: Single tenant, up to 100,000 stored log entries with
<2s UI load time

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Evidence |
|-----------|--------|----------|
| I. Speed Above All | PASS | SSE chunks forwarded via manual read/write/flush loop with `http.ResponseController` (not io.Copy — no flush control). `io.TeeReader` captures stream for logging without extra copies. Logging is async (non-blocking). Benchmarks required for proxy handler, stream relay, translation. |
| II. Efficient Use of Resources | PASS | Single static binary (CGO_ENABLED=0). SQLite embedded (no external DB process). Minimal dependency set justified by concrete needs. Linear scaling via goroutine-per-request. Target: 2 vCPU / 2 GB. |
| III. Clean Abstractions | PASS | Provider interface in `internal/provider/provider.go`. Anthropic implementation in `internal/provider/anthropic/`. Translation logic in pure functions (`internal/translate/`). Separate packages for auth, proxy, logging, pricing, storage, web. |
| IV. Correctness and Compatibility | PASS | Contract tests against Anthropic and OpenAI API schemas. Faithful request/response proxying. Error codes preserved. SSE events forwarded without transformation for Anthropic pass-through. |
| V. Security by Default | PASS | Upstream TLS enforced. Proxy API key auth required on all requests. Master key required for web UI with session token exchange. API keys redacted in logs. gosec + govulncheck in CI/pre-commit. |

No violations. Gate passed.

## Project Structure

### Documentation (this feature)

```text
specs/001-core-proxy-logging/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   ├── anthropic-proxy.md
│   ├── openai-proxy.md
│   └── admin-api.md
└── tasks.md             # Phase 2 output (/speckit.tasks)
```

### Source Code (repository root)

```text
main.go                        # entry point, calls cmd.Execute()
cmd/
├── root.go                    # cobra root command, viper config init
├── serve.go                   # 'serve' command, starts proxy server
└── keys.go                    # 'keys create|list|revoke' subcommands

internal/
├── config/
│   └── config.go              # config types, loading, validation
├── auth/
│   ├── keys.go                # proxy API key hashing, verification
│   └── session.go             # web UI session token mgmt (in-memory)
├── proxy/
│   ├── handler.go             # HTTP handler: resolve model, dispatch
│   └── stream.go              # SSE stream relay with tee logging
├── provider/
│   ├── provider.go            # Provider interface definition
│   └── anthropic/
│       ├── client.go          # Anthropic upstream HTTP client
│       └── types.go           # Anthropic request/response types
├── translate/
│   ├── openai_to_anthropic.go # OpenAI → Anthropic request mapping
│   └── anthropic_to_openai.go # Anthropic → OpenAI response mapping
├── models/
│   └── types.go               # shared types: RequestLog, ModelMapping
├── pricing/
│   └── calculator.go          # cost from token counts + model prices
├── store/
│   ├── store.go               # Store interface (queries)
│   ├── sqlite.go              # SQLite implementation
│   └── migrations/
│       └── *.sql              # goose migration files (embedded)
└── web/
    ├── handlers.go            # UI route handlers (login, logs, costs)
    ├── middleware.go          # session auth middleware
    ├── templates/             # Go html/template files (go:embed)
    └── static/                # CSS, JS assets (go:embed)

queries/
└── queries.sql                # sqlc query definitions

sqlc.yaml                      # sqlc configuration
Makefile                       # build, test, lint, audit targets
.goreleaser.yaml               # release configuration
.golangci.yml                  # linter configuration
go.mod                         # codeberg.org/kglitchy/llm-proxy
```

**Structure Decision**: Single Go project with `internal/` packages
following standard Go layout. The web UI is embedded in the binary via
`go:embed` — no separate frontend build or deployment. Cobra commands
in `cmd/` provide the CLI surface. All private logic lives under
`internal/` to enforce package boundaries.

## Complexity Tracking

No constitution violations to justify.
