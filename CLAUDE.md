# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Commands

```bash
# Build and test
make build              # Build binary to ./glitchgate
make test               # Run all tests with race detection
make lint               # Run golangci-lint (staticcheck, gosec, errcheck, revive)
make audit              # Run govulncheck for security (gosec runs via golangci-lint)

# Run specific tests
go test -race ./internal/proxy/...                    # Run proxy tests only
go test -race ./internal/proxy -run TestHandler         # Run specific test
go test -race ./internal/proxy -v                       # Verbose test output

# Generate code (deprecated - sqlc removed)
make generate           # DEPRECATED: sqlc was removed; no longer generates Go types

# Run locally
./glitchgate serve      # Start server (requires config.yaml with master_key)
```

## Config Location

The server looks for `config.yaml` in (order of precedence):
1. Current working directory
2. `~/.config/glitchgate/config.yaml`
3. `/etc/glitchgate/config.yaml`

For development, copy `config.example.yaml` to `config.yaml` and set `master_key`.

## Architecture Overview

glitchgate is an LLM API reverse proxy with format translation between three API styles:
- **Anthropic Messages API** (`/anthropic/v1/messages`)
- **OpenAI Chat Completions API** (`/openai/v1/chat/completions`)
- **OpenAI Responses API** (`/openai/v1/responses`)
- **Google Gemini API** (`/v1beta/models/{model}:generateContent`)

Request flow: Client → Proxy Handler → Format Translation → Upstream Provider → Cost Logging → SQLite

### Key Architectural Layers

**proxy/** - Core HTTP handlers in `handler.go`, `openai_handler.go`, `responses_handler.go`. Handles:
- Fallback chains for model routing (retries on 5xx/429)
- Streaming: SSE passthrough via `stream.go` or synthesized via `SynthesizeAnthropicSSE`
- Pipeline execution in `pipeline.go` orchestrates translation → provider → logging

**translate/** - Pure functions for API format conversion. Uses a canonical Intermediate Representation (IR) as the pivot format:
- `canonical.go` - Central IR type that all API formats convert to/from
- `delegations.go` - Routes translation based on source/target format
- `anthropic_to_openai.go` / `openai_to_anthropic.go`
- `responses_to_anthropic.go` / `anthropic_to_responses.go`
- `stream_translator.go` - SSE parsers for streaming responses
- `reverse_stream.go` - Reverse translation for streaming

**provider/** - Interface for upstream LLM services (`Provider` interface in `provider.go`):
- `anthropic.Client` - Native Anthropic API
- `openai.Client` - OpenAI-compatible APIs
- `gemini.Client` - Google Gemini API
- `copilot.Client` - GitHub Copilot via OAuth
- `vertex.Client` - Google Vertex AI Claude client
Each implements `SendRequest()` and reports native `APIFormat()` ("anthropic", "openai", "responses", or "gemini")

**store/** - SQLite/Postgres data access with composable interfaces (`store.go`):
- `SQLiteStore` implements `ProxyKeyStore`, `RequestLogStore`, `UserAdminStore`, etc.
- Raw `database/sql` with inline SQL in `sqlite_*.go` and `postgres_*.go`
- Migrations in `store/migrations/*.sql` applied via goose
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

**pricing/** - Model-to-cost mapping with built-in defaults for Anthropic, OpenAI, Gemini, Copilot. Metadata overrides via config `model_list` entries.

### OpenAI Streaming API Notes

Tool calls in OpenAI SSE streaming work differently than Anthropic:
- **Initial chunk**: Contains full tool call with `id`, `name`, and empty `arguments`
- **Delta chunks**: Only include `index` and incremental `arguments` delta - NOT the accumulated full string
- Do NOT accumulate and re-emit full arguments on each delta (common mistake)

See `stream_translator.go:writeToolCallDeltaChunk` for correct implementation.

### Database

The project uses raw `database/sql` with inline SQL in `sqlite_*.go` and `postgres_*.go` files. Migrations in `store/migrations/*.sql` are applied via goose.

### Testing Patterns

- Table-driven tests with `testify/require`
- `proxy/handler_test.go` shows `newTestHarness` pattern for full-stack handler tests
- Use `t.TempDir()` for isolated test databases
- Call `logger.Close()` to drain async logger in tests

### Security Requirements

Security tools run in CI and pre-commit:
- `golangci-lint` with `gosec` linter enabled - SAST (static application security testing)
- `govulncheck` - SCA (software composition analysis)

If a finding is a false positive, justify the exclusion in a comment and raise it to the user.

## Project Structure

```
cmd/                    # cobra commands (root, serve, keys, auth)
internal/
├── app/                # Runtime bootstrap, provider factory, logging setup
├── auth/               # Proxy key hashing + UI session management
├── config/             # Viper config loading with model resolution
├── pricing/            # Cost calculation with built-in rate tables
├── provider/           # Provider interface
│   ├── anthropic/      # Anthropic API client
│   ├── openai/         # OpenAI-compatible client (Azure, Ollama, etc.)
│   ├── gemini/         # Google Gemini API client
│   ├── copilot/        # GitHub Copilot OAuth client
│   └── vertex/         # Google Vertex AI Claude client
├── proxy/              # Core proxy handlers + SSE streaming + pipeline
├── store/              # SQLite + Postgres data access + migrations
│   └── migrations/    # goose migration files
├── translate/          # API format translation (3×3 matrix via canonical IR)
└── web/               # UI handlers, templates, embedded assets
specs/                 # Feature specifications organized by number
```

## Feature Specifications

Features are specced in `specs/NNN-feature-name/` with:
- `spec.md` - Requirements and acceptance criteria
- `plan.md` - Implementation plan
- `tasks.md` - Actionable tasks

Check `specs/ROADMAP.md` for planned features.

## Active Technologies
- Go (latest stable, currently 1.24) + `cobra`+`viper` (CLI/config), `chi/v5` (router), `golang.org/x/oauth2` (Vertex auth) (001-model-discovery)
- SQLite via `modernc.org/sqlite` — not impacted by this feature (001-model-discovery)

## Recent Changes
- 001-model-discovery: Added Go (latest stable, currently 1.24) + `cobra`+`viper` (CLI/config), `chi/v5` (router), `golang.org/x/oauth2` (Vertex auth)

## Release Procedure

To create and push a new version:

```bash
# 1. Create an annotated tag (use next sequential version)
git tag -a v0.0.16 -m "v0.0.16 - <brief description>"

# 2. Build multi-arch image and push with version tag
make image-push-version
```

This builds a multi-arch manifest (linux/amd64, linux/arm64) and pushes:
- `ghcr.io/seckatie/glitchgate:latest`
- `ghcr.io/seckatie/glitchgate:v0.0.16`

The VERSION is automatically derived from the tag via `git describe --tags --always`.
