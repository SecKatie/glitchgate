# Research: Automatic Model Discovery

**Feature**: 001-model-discovery | **Date**: 2026-03-20

## R1: Provider Model Listing APIs

### Decision: Use each provider's native listing endpoint

**Rationale**: Every supported provider already exposes a model listing API that uses the same auth mechanism as inference requests. No additional credentials or dependencies required.

**Alternatives considered**:
- Hardcoded model lists — rejected (defeats the purpose of discovery; stale immediately)
- Single unified API adapter — rejected (each provider's response shape differs significantly; unnecessary abstraction)

### Per-Provider Details

#### Anthropic (direct)

- **Endpoint**: `GET https://api.anthropic.com/v1/models`
- **Auth**: `X-Api-Key` header + `anthropic-version: 2023-06-01`
- **Pagination**: Cursor-based (`after_id`, `limit` 1–1000, default 20)
- **Response**: `{ "data": [{ "id", "display_name", "created_at" }], "has_more", "last_id" }`
- **Key field for discovery**: `id` → becomes `upstream_model`

#### Anthropic (Vertex AI)

- **Endpoint**: `GET https://{region}-aiplatform.googleapis.com/v1beta1/publishers/anthropic/models`
- **Auth**: OAuth2 bearer token (same `tokenSource` as inference)
- **Pagination**: Page-token based (`pageSize`, `pageToken`)
- **Response**: `{ "publisherModels": [{ "name": "publishers/anthropic/models/{id}" }] }`
- **Key field**: Extract model ID from `name` suffix
- **Note**: v1beta1 only (v1 has GET single model but no list)

#### OpenAI

- **Endpoint**: `GET https://api.openai.com/v1/models` (or `{baseURL}/v1/models` for compatible services)
- **Auth**: `Authorization: Bearer $KEY`
- **Pagination**: None (returns all models in one response)
- **Response**: `{ "data": [{ "id", "owned_by", "created" }] }`
- **Key field**: `id` → becomes `upstream_model`

#### Gemini (direct / API key)

- **Endpoint**: `GET https://generativelanguage.googleapis.com/v1beta/models?key=$KEY`
- **Auth**: Query parameter `key`
- **Pagination**: Page-token based (`pageSize` max 1000, `pageToken`)
- **Response**: `{ "models": [{ "name": "models/{id}", "displayName", "supportedGenerationMethods" }] }`
- **Key field**: Strip `models/` prefix from `name` → becomes `upstream_model`
- **Filter note**: Only include models with `"generateContent"` in `supportedGenerationMethods` (excludes embedding-only models)

#### Gemini (Vertex AI)

- **Endpoint**: `GET https://{region}-aiplatform.googleapis.com/v1beta1/publishers/google/models`
- **Auth**: OAuth2 bearer token (same `tokenSource` as inference)
- **Pagination**: Page-token based
- **Response**: `{ "publisherModels": [{ "name": "publishers/google/models/{id}" }] }`
- **Key field**: Extract model ID from `name` suffix

---

## R2: Discovery Integration Point in Config Loading

### Decision: Call discovery after provider construction, before `buildResolvedChains`

**Rationale**: Discovery needs constructed provider clients (for auth/HTTP), and results must be injected into `ModelList` before chain resolution. The `Load()` function in `config.go` currently calls `buildResolvedChains()` at the end — discovery injects synthetic `ModelMapping` entries just before that call.

**Alternatives considered**:
- Discovery inside `buildResolvedChains` — rejected (mixes network I/O with pure validation logic, violates constitution principle III)
- Lazy discovery on first request — rejected (violates fail-fast principle; unpredictable startup behavior)
- Separate goroutine with channel — rejected (unnecessary complexity for startup-only operation)

### Integration Flow

1. `config.Load()` unmarshals config as today
2. `config.Load()` calls `app.BuildProviders()` (or similar) to construct provider clients
3. New `config.DiscoverModels(providers)` iterates providers with `discover_models: true`
4. For each: type-assert to `ModelDiscoverer`, call `ListModels(ctx)`, apply `discover_filter`, generate `ModelMapping` entries
5. Append discovered entries to `cfg.ModelList` (skip if `model_name` already exists — explicit precedence)
6. `buildResolvedChains()` runs as before

**Problem**: Currently `config.Load()` returns `*Config` which is then used by `app` to build providers. Discovery needs providers to already exist. This creates a circular dependency.

**Resolution**: Split into two phases:
1. `config.Load()` returns config as today (no discovery yet)
2. `app.Bootstrap()` builds providers, then calls `cfg.InjectDiscoveredModels(providers)` which does discovery + rebuilds resolved chains

---

## R3: ModelDiscoverer Interface Design

### Decision: Optional interface on provider.Provider via type assertion

**Rationale**: Not all providers support discovery. Using a separate optional interface (like `io.Closer`) keeps the core `Provider` interface unchanged and allows compile-time checking per provider.

**Alternatives considered**:
- Add `ListModels` to core `Provider` interface — rejected (breaks copilot which can't implement it; forces stub methods)
- Registry/factory pattern — rejected (over-engineering for 4 implementations)

### Interface

```go
// ModelDiscoverer is an optional interface that providers can implement
// to support automatic model discovery at startup.
type ModelDiscoverer interface {
    ListModels(ctx context.Context) ([]DiscoveredModel, error)
}

type DiscoveredModel struct {
    ID          string // upstream model identifier (e.g., "claude-sonnet-4-6")
    DisplayName string // optional human-readable name
}
```

---

## R4: Discover Filter Implementation

### Decision: Use `filepath.Match` (glob) semantics with `!` prefix for exclusion

**Rationale**: Aligns with existing wildcard conventions in `model_list` (`prefix/*`). `filepath.Match` is stdlib, zero dependencies, and sufficient for model ID patterns.

**Alternatives considered**:
- Regex — rejected (overkill, error-prone for config files)
- Exact string lists — rejected (too verbose for many models)

### Semantics

- `discover_filter: ["claude-*"]` — only include models matching `claude-*`
- `discover_filter: ["*", "!*-preview"]` — include all, exclude preview models
- Empty/absent `discover_filter` — include all discovered models
- Exclude patterns (prefixed with `!`) take precedence over include patterns
- Matching is applied against the upstream model ID (before prefix is added)

---

## R5: Vertex AI Listing Endpoint Stability

### Decision: Use v1beta1 endpoint for Vertex model listing with awareness of instability

**Rationale**: The Vertex AI model listing endpoint (`/v1beta1/publishers/{publisher}/models`) is only available in beta. The GA v1 API only supports getting a single model by name. Since we degrade gracefully on failure (log warning, continue without), beta instability is acceptable.

**Alternatives considered**:
- Skip Vertex discovery entirely — rejected (user explicitly requested vertex support in clarification)
- Use v1 single-model GET with known model list — rejected (defeats purpose of auto-discovery)

---

## R6: Startup Timeout for Discovery

### Decision: Use existing `upstream_request_timeout` config value (default 30s) per provider listing call

**Rationale**: Reuses existing timeout configuration. Each provider's listing call is independent and bounded. Total discovery time is bounded by `max(per-provider timeout) * num_discovery_providers`, which with typical 2-3 providers stays well under the 2s target for fast APIs and degrades gracefully for slow ones.

**Alternatives considered**:
- Dedicated `discovery_timeout` config — rejected (unnecessary config surface for startup-only operation)
- Global discovery deadline — rejected (one slow provider shouldn't cancel fast ones)
- Parallel discovery with `errgroup` — worth considering if serial discovery exceeds 2s target; defer to implementation
