# llm-proxy Development Guidelines

Auto-generated from all feature plans. Last updated: 2026-03-11

## Active Technologies
- Go 1.24+ + chi/v5 (HTTP router), go-resty/v3 (upstream calls), modernc.org/sqlite (pure-Go SQLite), goose/v3 (migrations), testify/require (tests) (003-cache-token-logging)
- SQLite — one new migration (`003_add_cache_tokens.sql`) adding two columns to `request_logs` (003-cache-token-logging)
- Go 1.24+ + HTMX 2.0.4 (CDN), Pico CSS v2 (CDN), standard library `encoding/json`, `internal/pricing`, `internal/store`, `internal/provider/anthropic` (004-ui-log-improvements)
- SQLite — no schema changes; one new store method (`CountLogsSince`) (004-ui-log-improvements)

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
- 004-ui-log-improvements: Added Go 1.24+ + HTMX 2.0.4 (CDN), Pico CSS v2 (CDN), standard library `encoding/json`, `internal/pricing`, `internal/store`, `internal/provider/anthropic`
- 003-cache-token-logging: Added Go 1.24+ + chi/v5 (HTTP router), go-resty/v3 (upstream calls), modernc.org/sqlite (pure-Go SQLite), goose/v3 (migrations), testify/require (tests)

- 002-wildcard-model-routing: Wildcard prefix model routing in config
  monitoring, web UI

<!-- MANUAL ADDITIONS START -->
<!-- MANUAL ADDITIONS END -->
