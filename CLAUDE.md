# glitchgate Development Guidelines

Auto-generated from all feature plans. Last updated: 2026-03-11

## Active Technologies
- Go 1.24+ + chi/v5 (HTTP router), go-resty/v3 (upstream calls), modernc.org/sqlite (pure-Go SQLite), goose/v3 (migrations), testify/require (tests) (003-cache-token-logging)
- SQLite — one new migration (`003_add_cache_tokens.sql`) adding two columns to `request_logs` (003-cache-token-logging)
- Go 1.24+ + HTMX 2.0.4 (CDN), Pico CSS v2 (CDN), standard library `encoding/json`, `internal/pricing`, `internal/store`, `internal/provider/anthropic` (004-ui-log-improvements)
- SQLite — no schema changes; one new store method (`CountLogsSince`) (004-ui-log-improvements)
- Go 1.24+ + chi/v5 (router), HTMX 2.0.4 (CDN), Pico CSS v2 (CDN), cobra/viper (CLI), modernc.org/sqlite (005-key-management-ui)
- SQLite via modernc.org/sqlite — one new migration for `audit_events` table (005-key-management-ui)
- Go 1.24+ + cobra/viper (CLI), chi/v5 (router), net/http (OAuth + Copilot API calls), encoding/json (006-github-copilot-provider)
- Filesystem (JSON token files with 0600 permissions); existing SQLite for request logging (no schema changes) (006-github-copilot-provider)
- Go 1.26.1 (module: `codeberg.org/kglitchy/glitchgate`) (007-implement-oidc)
- SQLite via `modernc.org/sqlite` — 8 new migrations (006–013) (007-implement-oidc)
- Go 1.26.1 (module `codeberg.org/kglitchy/glitchgate`) + chi/v5, go-resty/v3, cobra+viper, modernc.org/sqlite, goose/v3, testify/require (008-model-fallback)
- SQLite — one new migration (`014_add_fallback_attempts.sql`) (008-model-fallback)
- Go 1.26.1 (module `codeberg.org/kglitchy/glitchgate`) + chi/v5 (router), go-resty/v3 (upstream calls), cobra+viper (CLI/config), modernc.org/sqlite (storage), goose/v3 (migrations), testify/require (tests) (010-responses-api-support)
- SQLite — no schema changes; existing `request_logs` table supports new `source_format` value `"responses"` (010-responses-api-support)
- Go 1.26.1 + chi/v5 (router), HTMX 2.0.4 (CDN), Pico CSS v2 (CDN), internal/pricing, internal/config, internal/store (011-models-page)
- SQLite (read-only for this feature; no schema changes) (011-models-page)

- Go 1.24+ with cobra + viper (CLI/config), chi/v5 (HTTP router)
- net/http (upstream SSE streaming), go-resty/v3 (non-streaming calls)
- SQLite via modernc.org/sqlite (pure Go, no CGO)
- goose/v3 (migrations), sqlc (query generation)
- HTMX + Pico CSS (embedded web UI via go:embed)
- testify/require (testing)

## Project Structure

```text
main.go
cmd/                    # cobra commands (root, serve, keys)
internal/
├── config/             # viper config loading
├── auth/               # proxy key + session management
├── proxy/              # core proxy handler + SSE streaming
├── provider/           # provider interface
│   └── anthropic/      # Anthropic implementation
├── translate/          # OpenAI ↔ Anthropic translation
├── models/             # shared internal types
├── pricing/            # cost calculation
├── store/              # SQLite data access + migrations
└── web/                # UI handlers, templates, static assets
queries/                # sqlc SQL files
```

## Commands

```bash
make build              # build the binary
make test               # go test -race ./...
make lint               # golangci-lint run
make audit              # gosec + govulncheck
```

## Code Style

- Format with gofumpt + goimports
- Lint with golangci-lint v2 (staticcheck, gosec, errcheck, revive)
- Table-driven tests with testify/require
- Use net/http.Client for SSE streaming, go-resty for non-streaming
- Provider interfaces in internal/provider/provider.go
- Translation as pure functions in internal/translate/

## Recent Changes
- 011-models-page: Added Go 1.26.1 + chi/v5 (router), HTMX 2.0.4 (CDN), Pico CSS v2 (CDN), internal/pricing, internal/config, internal/store
- 010-responses-api-support: Added Go 1.26.1 (module `codeberg.org/kglitchy/glitchgate`) + chi/v5 (router), go-resty/v3 (upstream calls), cobra+viper (CLI/config), modernc.org/sqlite (storage), goose/v3 (migrations), testify/require (tests)
- 008-model-fallback: Added Go 1.26.1 (module `codeberg.org/kglitchy/glitchgate`) + chi/v5, go-resty/v3, cobra+viper, modernc.org/sqlite, goose/v3, testify/require

  monitoring, web UI

<!-- MANUAL ADDITIONS START -->
## Architecture Overview

glitchgate is an LLM API reverse proxy that handles format translation between three API styles:
- **Anthropic Messages API** (`/v1/messages`)
- **OpenAI Chat Completions API** (`/v1/chat/completions`)
- **OpenAI Responses API** (`/v1/responses`)

Request flow: Client → Proxy Handler → Format Translation → Upstream Provider → Cost Logging → SQLite

Key architectural layers:

- **proxy/** - Core HTTP handlers for `/v1/*` endpoints. Handles fallback chains, streaming (SSE relay vs synthesis), and orchestrates translation. Main entry point: `ServeHTTP` in `handler.go`

- **translate/** - Pure functions for API format conversion (Anthropic ↔ OpenAI ↔ Responses). Each direction has request/response translators. Streaming uses specialized SSE parsers/relays (`stream_translator`, `reverse_stream`, `responses_stream_translator`)

- **provider/** - Interface for upstream LLM services (`Provider` interface). Implementations: `anthropic.Client`, `openai.Client`, `copilot.Client`. Each provider implements `SendRequest()` and reports its native `APIFormat()` ("anthropic", "openai", or "responses")

- **store/** - SQLite data access via `Store` interface. Uses sqlc for query generation from `queries/*.sql`. Migrations embedded in `store/migrations/` and applied via `goose`. All DB operations return Go structs (typed) - no raw SQL in handlers

- **config/** - Viper-based config with `model_list` for client-facing model routing. Uses wildcard prefix matching (`prefix/*`) and virtual model fallback chains with cycle detection

- **web/** - Embedded HTMX + Pico CSS UI via `go:embed`. Session-based auth with OIDC support. Templates use cloned base to avoid block collisions (ParseTemplates)

- **pricing/** - Model-to-cost mapping with built-in defaults for Anthropic, OpenAI, Copilot. Metadata overrides via config `model_list` entries

## Key Patterns

- **Fallback chains**: `Config.FindModel(modelName)` returns ordered dispatch slice. Main handler iterates, retrying on 5xx/429 and logging each attempt count via `fallback_attempts`

- **Format-aware routing**: Provider's `APIFormat()` determines if translation is needed. If "openai" or "responses", handler delegates to `serveViaOpenAIProvider`/`serveViaResponsesProvider` which call translation then pass to provider

- **Streaming**: net/http for upstream SSE; `RelaySSEStream` for passthrough, `SynthesizeAnthropicSSE` for forced non-streaming clients. Response struct has `Stream io.ReadCloser` vs `Body []byte`

- **Async logging**: `AsyncLogger` batches writes to SQLite in background goroutine with channel buffer (default 1000). Handler sends `RequestLogEntry` and returns immediately

- **SQLC workflow**: Edit `.sql` files in `queries/`, run `make generate` to update Go types in `store/`. Migrations in `store/migrations/` are embedded as `var migrations embed.FS`

- **Template clones**: Each page template is cloned from base to let multiple pages define same block names (title, content, head) without collision. Fragments (`templates/fragments/*.html`) are shared across all pages

## Adding a New Provider

1. Implement `Provider` interface in `internal/provider/<providername>/client.go`
2. Create types.go if struct definitions needed
3. Add to provider config map in `cmd/serve.go` (switch statement)
4. If new API format, add translation functions in `translate/`

## Adding Database Queries

1. Add SQL to `queries/*.sql`
2. Run `make generate` (sqlc generates Go in `internal/store/`)
3. Add method to `Store` interface
4. Implement in SQLite struct

## Session & Auth Context

- `Session` from context via `auth.SessionFromContext(r.Context())`
- Roles: `global_admin`, `team_admin`, `member`
- Scope enforcement via `store.ListProxyKeysByOwner`/`Store.ListProxyKeysByTeam`
- Middleware: `web.UISessionMiddleware` + `web.RequireGlobalAdmin` / `web.RequireAdminOrTeamAdmin`
<!-- MANUAL ADDITIONS END -->
