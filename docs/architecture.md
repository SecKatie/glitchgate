# Glitchgate Architecture

Glitchgate is an LLM API reverse proxy that handles format translation between three API styles: Anthropic Messages API, OpenAI Chat Completions API, and OpenAI Responses API.

## Table of Contents

1. [Overview](#overview)
2. [Project Structure](#project-structure)
3. [Request Flow](#request-flow)
4. [Core Components](#core-components)
   - [CLI & Configuration](#cli--configuration)
   - [HTTP Router & Middleware](#http-router--middleware)
   - [Proxy Handlers](#proxy-handlers)
   - [Translation Layer](#translation-layer)
   - [Provider System](#provider-system)
   - [Storage Layer](#storage-layer)
   - [Web UI](#web-ui)
   - [Pricing & Cost Calculation](#pricing--cost-calculation)
5. [Key Patterns](#key-patterns)
6. [Extending Glitchgate](#extending-glitchgate)

---

## Overview

Glitchgate acts as a unified API gateway for multiple LLM providers. It accepts requests in any supported format, translates them to the provider's native format, forwards the request, and translates the response back to the client's expected format.

**Key capabilities:**
- Multi-format API support (Anthropic, OpenAI Chat Completions, OpenAI Responses)
- Automatic format translation
- Fallback chains with retry logic
- Streaming support (SSE pass-through and synthesis)
- Async request logging with cost tracking
- Web UI for logs, costs, model management, and team administration
- OIDC authentication with role-based access control

---

## Project Structure

```
glitchgate/
├── main.go                      # Entry point
├── cmd/                         # CLI commands (cobra)
│   ├── root.go                  # Root command + config file flag
│   ├── serve.go                 # Main server command
│   ├── keys.go                  # Proxy key management CLI
│   ├── auth.go                  # Auth command group
│   └── auth_copilot.go          # GitHub Copilot OAuth flow
├── internal/
│   ├── app/                     # Runtime bootstrapping
│   │   ├── runtime.go           # Bootstrap function, Runtime struct
│   │   └── providers.go         # Provider registry
│   ├── auth/                    # Authentication utilities
│   │   ├── session.go           # UI session store
│   │   ├── keys.go              # Key generation/verification
│   │   └── context.go           # Context helpers
│   ├── config/                  # Configuration loading (viper)
│   ├── proxy/                   # Core proxy handlers
│   │   ├── handler.go           # Anthropic Messages handler
│   │   ├── openai_handler.go    # OpenAI Chat Completions handler
│   │   ├── responses_handler.go # OpenAI Responses API handler
│   │   ├── pipeline.go          # Fallback chain execution
│   │   ├── middleware.go        # Auth & rate limit middleware
│   │   ├── logger.go            # AsyncLogger
│   │   └── stream.go            # SSE streaming utilities
│   ├── provider/                # Provider interface + implementations
│   │   ├── provider.go          # Provider interface
│   │   ├── anthropic/           # Anthropic Messages API client
│   │   ├── openai/              # OpenAI-compatible client
│   │   └── copilot/             # GitHub Copilot client
│   ├── translate/               # API format translation
│   ├── store/                   # SQLite data access layer
│   │   ├── store.go             # Store interface
│   │   ├── sqlite.go            # SQLiteStore implementation
│   │   ├── sqlite_*.go          # Narrow interface implementations
│   │   └── migrations/          # goose migrations
│   ├── web/                     # Web UI handlers + templates
│   ├── oidc/                    # OIDC provider wrapper
│   ├── pricing/                 # Cost calculation
│   ├── ratelimit/               # Token-bucket rate limiting
│   └── models/                  # Shared types
├── queries/                     # sqlc SQL files
└── go.mod
```

---

## Request Flow

```
Client (any format)
        │
        ▼
┌─────────────────────────────────────────────────────────────┐
│                      chi router (/v1/*)                      │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │  Middleware Stack:                                      │ │
│  │  1. RealIP       - Extract client IP                    │ │
│  │  2. Recoverer    - Panic recovery                       │ │
│  │  3. SecurityHeaders - CSP, XSS protection               │ │
│  │  4. IPRateLimit  - Per-IP rate limiting                 │ │
│  │  5. Auth         - Proxy key validation                 │ │
│  │  6. KeyRateLimit - Per-key rate limiting                │ │
│  └─────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────┐
│                    Format Handler                            │
│  (AnthropicHandler / OpenAIHandler / ResponsesHandler)      │
│                                                              │
│  1. Parse request body, extract model name                   │
│  2. config.FindModel() → dispatch chain                     │
│  3. executeFallbackChain()                                   │
└─────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────┐
│                  Fallback Chain Execution                    │
│                                                              │
│  For each model in dispatch chain:                          │
│  ┌────────────────────────────────────────────────────────┐ │
│  │  1. Build route plan (translate if needed)             │ │
│  │  2. provider.SendRequest()                             │ │
│  │  3. Handle response (translate back if needed)         │ │
│  │  4. On success: log + return                           │ │
│  │  5. On 5xx/429: retry next model in chain              │ │
│  │  6. On other error: log + return error                 │ │
│  └────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────┐
│                    AsyncLogger                               │
│  • Channel buffer (default: 1000)                           │
│  • Background goroutine writes batches                      │
│  • Logs: tokens, latency, status, bodies, errors            │
└─────────────────────────────────────────────────────────────┘
        │
        ▼
    Response to client
```

---

## Core Components

### CLI & Configuration

**Entry Point** (`main.go`):
```go
func main() {
    cmd.Execute()
}
```

**Commands** (cobra):
- `glitchgate serve` - Start HTTP server
- `glitchgate keys create/list/delete/update` - Proxy key management
- `glitchgate auth copilot` - GitHub Copilot OAuth flow

**Configuration** (viper):
- Default config path: `~/.config/glitchgate/config.yaml`
- Override with `--config <path>` or `GLITCHGATE_*` env vars

**Key config sections**:
```yaml
listen: ":4000"
database_path: "glitchgate.db"
timezone: "America/Los_Angeles"

providers:
  - name: anthropic
    type: anthropic
    base_url: "https://api.anthropic.com"
    auth_mode: proxy_key

model_list:
  - model_name: claude-4
    fallbacks:
      - gc/claude-opus-4-6
      - gc/claude-sonnet-4-6

  - model_name: gc/*
    provider: anthropic
    upstream_model: ""  # suffix becomes upstream model
```

### HTTP Router & Middleware

**Router** (chi/v5) in `cmd/serve.go`:

```go
r := chi.NewRouter()
r.Use(chimw.RealIP)
r.Use(chimw.Recoverer)
r.Use(web.SecurityHeadersMiddleware)
```

**Route Groups**:

| Prefix | Purpose | Middleware |
|--------|---------|------------|
| `/v1/messages` | Anthropic API | IP rate limit, Auth, Key rate limit |
| `/v1/chat/completions` | OpenAI API | IP rate limit, Auth, Key rate limit |
| `/v1/responses` | Responses API | IP rate limit, Auth, Key rate limit |
| `/ui/*` | Web UI | Session, Role checks |
| `/ui/auth/*` | Auth endpoints | Login rate limit |

### Proxy Handlers

Three handlers for each API format, all following the same pattern:

1. **`handler.go`** - Anthropic Messages (`/v1/messages`)
2. **`openai_handler.go`** - OpenAI Chat Completions (`/v1/chat/completions`)
3. **`responses_handler.go`** - OpenAI Responses (`/v1/responses`)

**Handler responsibilities**:
- Parse request body
- Resolve model via `config.FindModel()`
- Execute fallback chain via `executeFallbackChain()`
- Stream or return response

**Pipeline** (`pipeline.go`):
- `executeFallbackChain()` - Iterates dispatch chain, retries on 5xx/429
- `executeProviderAttempt()` - Single provider request
- `routeBuilder` - Creates translation plan based on provider's `APIFormat()`

### Translation Layer

Pure functions in `internal/translate/` for format conversion:

| From | To | File |
|------|-----|------|
| Anthropic | OpenAI | `anthropic_to_openai*.go` |
| OpenAI | Anthropic | `openai_to_anthropic*.go` |
| Anthropic | Responses | `anthropic_to_responses.go` |
| Responses | Anthropic | `responses_to_anthropic*.go` |
| OpenAI | Responses | `openai_to_responses.go` |
| Responses | OpenAI | `responses_to_openai.go` |

**Streaming translators**:
- `RelaySSEStream()` - Pass-through for matching formats
- `ReverseSSEStream()` - OpenAI SSE → Anthropic SSE
- `SynthesizeAnthropicSSE()` - Convert non-streaming to SSE

**Extended thinking**:
- `thinking.go` - Handles reasoning token translation between providers

### Provider System

**Interface** (`provider/provider.go`):
```go
type Provider interface {
    Name() string        // "anthropic", "openai", "copilot"
    AuthMode() string    // "proxy_key", "forward", "internal"
    APIFormat() string   // "anthropic", "openai", "responses"
    SendRequest(ctx context.Context, req *Request) (*Response, error)
}
```

**Implementations**:

| Provider | File | Auth Modes | API Formats |
|----------|------|------------|-------------|
| Anthropic | `provider/anthropic/client.go` | proxy_key, forward | anthropic |
| OpenAI | `provider/openai/client.go` | proxy_key, forward | openai, responses |
| GitHub Copilot | `provider/copilot/client.go` | internal | openai |

**Copilot specifics**:
- OAuth-based authentication (GitHub token → Copilot session)
- Tokens stored in filesystem with 0600 permissions
- Auto-refreshes expired session tokens
- Custom headers: Editor-Version, Copilot-Integration-Id

**Provider Registry** (`app/providers.go`):
- Maps provider name → Provider instance
- Stores pricing calculator reference
- Handles legacy provider aliases

### Storage Layer

**Database**: SQLite via `modernc.org/sqlite` (pure Go, no CGO)
- WAL mode for concurrent reads
- Foreign key constraints enforced

**Narrow interfaces** for dependency injection:

```go
type Store interface {
    UserAdminStore       // User CRUD
    TeamAdminStore       // Team management
    SessionReaderStore   // Session validation
    SessionBackendStore  // Session persistence
    ProxyKeyAuthStore    // Key lookup for auth
    RequestLogWriter     // AsyncLogger persistence
    ProxyKeyStore        // Full key CRUD
    RequestLogStore      // Log queries
    CostQueryStore       // Cost analytics
    ModelUsageStore      // Model statistics
    OIDCStateStore       // OIDC PKCE state
    OIDCUserStore        // OIDC user CRUD
    MaintenanceStore     // Cleanup operations
}
```

**Migrations** (goose):
- Embedded in binary via `//go:embed migrations`
- 20 migrations covering keys, logs, OIDC, teams, indexes

**Query generation** (sqlc):
- SQL files in `queries/*.sql`
- `make generate` creates Go types in `internal/store/`

### Web UI

**Tech stack**: HTMX 2.0.4 + Pico CSS v2 (CDN), embedded via `go:embed`

**Template system**:
- Base template with blocks: title, head, content
- Each page clones base to avoid block name collisions
- Fragments in `templates/fragments/*.html` for shared components

**Handlers**:
| File | Routes |
|------|--------|
| `handlers.go` | /ui/logs, /ui/models, /ui/keys, /ui/audit |
| `auth_handlers.go` | /ui/auth/login, /ui/auth/oidc, /ui/auth/callback |
| `cost_handlers.go` | /ui/costs, /ui/api/costs/timeseries |
| `user_handlers.go` | /ui/users (global admin only) |
| `team_handlers.go` | /ui/teams |

**Session & Auth**:
- Database-backed sessions with 8-hour TTL
- OIDC Authorization Code flow with PKCE
- Roles: `global_admin`, `team_admin`, `member`

### Pricing & Cost Calculation

**Calculator** (`pricing/calculator.go`):
```go
func (c *Calculator) Calculate(
    providerName, upstreamModel string,
    inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64,
) *float64
```

**Pricing sources**:
1. Built-in defaults (`pricing/defaults.go`) for Anthropic, OpenAI, Copilot
2. Config overrides via `model_list` metadata:
```yaml
model_list:
  - model_name: custom-model
    provider: openai
    upstream_model: gpt-4
    cost_per_million_input_tokens: 30.0
    cost_per_million_output_tokens: 60.0
```

---

## Key Patterns

### Async Logging

`proxy.AsyncLogger` prevents request blocking:

```go
type AsyncLogger struct {
    store   RequestLogWriter
    entries chan RequestLogEntry
    wg      sync.WaitGroup
}

func (l *AsyncLogger) logEntry(entry RequestLogEntry) {
    select {
    case l.entries <- entry:
    default:
        // Buffer full, drop entry (configurable behavior)
    }
}
```

### Fallback Chains

Model resolution returns pre-flattened dispatch chain:

```go
type ModelDispatch struct {
    ProviderName  string
    UpstreamModel string
    ModelName     string  // client-facing name
}

func (c *Config) FindModel(modelName string) ([]ModelDispatch, error)
```

Cycle detection prevents infinite loops in virtual model definitions.

### Dependency Injection

Narrow interfaces enable focused dependencies:

```go
// Handler only needs key lookup
func AuthMiddleware(store ProxyKeyAuthStore) func(http.Handler) http.Handler

// Web handlers need session + user data
func UISessionMiddleware(sessions *auth.UISessionStore, store SessionReaderStore)
```

### Streaming Strategy

| Client | Provider | Strategy |
|--------|----------|----------|
| Streaming | Same format | Pass-through relay |
| Streaming | Different format | Translate SSE in real-time |
| Non-streaming | Any | Normal request, optionally synthesize SSE |

---

## Extending Glitchgate

### Adding a New Provider

1. Implement `provider.Provider` interface in `internal/provider/<name>/client.go`
2. Create `types.go` if provider-specific structs needed
3. Add provider type to `internal/app/providers.go` switch statement
4. If new API format, add translation functions in `internal/translate/`

### Adding Database Queries

1. Add SQL to `queries/*.sql` with sqlc annotations
2. Run `make generate` to create Go types
3. Add method to appropriate narrow interface in `store/store.go`
4. Implement in `store/sqlite_*.go`

### Adding Web UI Pages

1. Create handler in `internal/web/`
2. Add route in `cmd/serve.go`
3. Create template in `internal/web/templates/`
4. Add fragment components in `templates/fragments/` as needed
