# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Commands

```bash
# Build and test
make build              # Build binary to ./glitchgate
make test               # Run all tests with race detection
make lint               # Run golangci-lint (staticcheck, gosec, errcheck, revive)
make audit              # Run gosec + govulncheck for security

# Run specific tests
go test -race ./internal/proxy/...                    # Run proxy tests only
go test -race ./internal/proxy -run TestHandler         # Run specific test
go test -race ./internal/proxy -v                       # Verbose test output

# Generate code (sqlc)
make generate           # Regenerate Go types from queries/*.sql

# Run locally
./glitchgate serve      # Start server (requires config.yaml with master_key)
```

## Architecture Overview

glitchgate is an LLM API reverse proxy with format translation between three API styles:
- **Anthropic Messages API** (`/v1/messages`)
- **OpenAI Chat Completions API** (`/v1/chat/completions`)
- **OpenAI Responses API** (`/v1/responses`)

Request flow: Client → Proxy Handler → Format Translation → Upstream Provider → Cost Logging → SQLite

### Key Architectural Layers

**proxy/** - Core HTTP handlers in `handler.go`, `openai_handler.go`, `responses_handler.go`. Handles:
- Fallback chains for model routing (retries on 5xx/429)
- Streaming: SSE passthrough via `stream.go` or synthesized via `SynthesizeAnthropicSSE`
- Pipeline execution in `pipeline.go` orchestrates translation → provider → logging

**translate/** - Pure functions for API format conversion. Each direction has request/response translators:
- `anthropic_to_openai.go` / `openai_to_anthropic.go`
- `responses_to_anthropic.go` / `anthropic_to_responses.go`
- `stream_translator.go` - SSE parsers for streaming responses
- `reverse_stream.go` - Reverse translation for streaming

**provider/** - Interface for upstream LLM services (`Provider` interface in `provider.go`):
- `anthropic.Client` - Native Anthropic API
- `openai.Client` - OpenAI-compatible APIs
- `copilot.Client` - GitHub Copilot via OAuth
Each implements `SendRequest()` and reports native `APIFormat()` ("anthropic", "openai", or "responses")

**store/** - SQLite data access with composable interfaces (`store.go`):
- `SQLiteStore` implements `ProxyKeyStore`, `RequestLogStore`, `UserAdminStore`, etc.
- Uses sqlc for type-safe queries from `queries/*.sql` (run `make generate` after changes)
- Migrations in `migrations/*.sql` applied via goose
- All DB operations return Go structs - no raw SQL in handlers

**config/** - Viper-based configuration with:
- `model_list` for client-facing model routing with wildcard prefix matching (`prefix/*`)
- Virtual model fallback chains with cycle detection
- `FindModel(modelName)` returns ordered dispatch slice for fallback chains

**web/** - Embedded HTMX + Pico CSS UI:
- Templates in `templates/` embedded via `go:embed`
- Template clones used to avoid block collisions (see `ParseTemplates`)
- Session-based auth with OIDC support
- Scope enforcement: `global_admin`, `team_admin`, `member` roles

**pricing/** - Model-to-cost mapping with built-in defaults for Anthropic, OpenAI, Copilot. Metadata overrides via config `model_list` entries.

### Database Workflow (sqlc)

1. Edit SQL in `queries/*.sql`
2. Run `make generate` to regenerate Go types
3. Add method to appropriate Store interface in `store.go`
4. SQLiteStore automatically implements interface via generated code

### Testing Patterns

- Table-driven tests with `testify/require`
- `proxy/handler_test.go` shows `newTestHarness` pattern for full-stack handler tests
- Use `t.TempDir()` for isolated test databases
- Call `logger.Close()` to drain async logger in tests

### Security Requirements

Security tools run in CI and pre-commit:
- `gosec` - SAST (static application security testing)
- `govulncheck` - SCA (software composition analysis)
- `golangci-lint` with gosec linter enabled

If a finding is a false positive, justify the exclusion in a comment and raise it to the user.

## Project Structure

```
cmd/                    # cobra commands (root, serve, keys, auth)
internal/
├── app/                # Runtime bootstrap and provider factory
├── auth/               # Proxy key hashing + UI session management
├── config/             # Viper config loading with model resolution
├── pricing/            # Cost calculation with built-in rate tables
├── provider/           # Provider interface
│   ├── anthropic/      # Anthropic API client
│   ├── openai/         # OpenAI-compatible client
│   └── copilot/        # GitHub Copilot OAuth client
├── proxy/              # Core proxy handlers + SSE streaming + pipeline
├── store/              # SQLite data access + migrations
│   └── migrations/     # goose migration files
├── translate/          # API format translation (3×3 matrix)
└── web/                # UI handlers, templates, embedded assets
queries/                # sqlc query files
specs/                  # Feature specifications organized by number
```

## Feature Specifications

Features are specced in `specs/NNN-feature-name/` with:
- `spec.md` - Requirements and acceptance criteria
- `plan.md` - Implementation plan
- `tasks.md` - Actionable tasks

Check `specs/ROADMAP.md` for planned features.
