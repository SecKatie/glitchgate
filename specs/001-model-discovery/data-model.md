# Data Model: Automatic Model Discovery

**Feature**: 001-model-discovery | **Date**: 2026-03-20

## New Types

### `provider.DiscoveredModel`

Represents a single model returned by a provider's listing endpoint.

| Field | Type | Description |
|-------|------|-------------|
| `ID` | `string` | Upstream model identifier (e.g., `claude-sonnet-4-6`, `gpt-4o`) |
| `DisplayName` | `string` | Optional human-readable name (e.g., `Claude Sonnet 4.6`) |

**Location**: `internal/provider/provider.go`

### `provider.ModelDiscoverer` (interface)

Optional interface for providers that support model listing.

```go
type ModelDiscoverer interface {
    ListModels(ctx context.Context) ([]DiscoveredModel, error)
}
```

**Implementors**: `anthropic.Client`, `openai.Client`, `gemini.Client`
**Non-implementors**: `copilot.Client`

## Modified Types

### `config.ProviderConfig`

Three new fields added:

| Field | Type | YAML Key | Default | Description |
|-------|------|----------|---------|-------------|
| `DiscoverModels` | `bool` | `discover_models` | `false` | Enable automatic model discovery for this provider |
| `ModelPrefix` | `*string` | `model_prefix` | `nil` (→ `{name}/`) | Client-facing prefix for discovered models. `nil` = use provider name. Empty string = no prefix. |
| `DiscoverFilter` | `[]string` | `discover_filter` | `nil` (→ include all) | Glob patterns for include/exclude filtering |

**Note on `ModelPrefix`**: Using `*string` distinguishes "not set" (use default `{name}/`) from "explicitly empty" (no prefix). This matches the existing `Stream *bool` pattern in `ProviderConfig`.

### `config.Config`

New method:

```go
// InjectDiscoveredModels queries providers that implement ModelDiscoverer,
// applies discover_filter, and prepends synthetic ModelMapping entries to
// ModelList before rebuilding resolvedChains.
func (c *Config) InjectDiscoveredModels(providers map[string]provider.Provider) error
```

**Behavior**:
1. Iterates `c.Providers` where `DiscoverModels == true`
2. Validates provider type supports discovery (fail startup if not)
3. Type-asserts provider to `ModelDiscoverer`
4. Calls `ListModels(ctx)` with timeout
5. Applies `DiscoverFilter` glob matching
6. For each passing model, creates `ModelMapping{ModelName: prefix+id, Provider: name, UpstreamModel: id}`
7. Skips if `ModelName` already exists in explicit `ModelList` (precedence rule)
8. Appends to `ModelList`, then calls `buildResolvedChains()` to rebuild

### `config.ModelMapping` (no structural change)

Discovered entries use the same `ModelMapping` struct as explicit entries. No `source` field needed — precedence is enforced at injection time (explicit entries already in `ModelList` block discovered duplicates).

## Entity Relationships

```text
Config
 ├── Providers[]  ──── ProviderConfig
 │                      ├── DiscoverModels: bool
 │                      ├── ModelPrefix: *string
 │                      └── DiscoverFilter: []string
 │
 ├── ModelList[]  ──── ModelMapping (explicit entries)
 │                      + ModelMapping (injected discovered entries)
 │
 └── resolvedChains ── map[string][]DispatchTarget
                        (rebuilt after injection)
```

## State Transitions

Discovery is a one-shot operation at startup. No runtime state changes.

```text
Config.Load()
  │
  ├── Unmarshal YAML
  ├── Validate providers (including DiscoverModels on unsupported → error)
  │
  ▼
app.Bootstrap()
  │
  ├── Build provider clients
  ├── Config.InjectDiscoveredModels(providers)
  │     │
  │     ├── For each provider with discover_models: true
  │     │     ├── ListModels(ctx) → []DiscoveredModel
  │     │     │     └── On error: log warning, skip provider
  │     │     ├── Apply discover_filter
  │     │     └── Inject ModelMapping entries (skip duplicates)
  │     │
  │     └── Rebuild resolvedChains
  │
  └── Start HTTP server
```

## Validation Rules

| Rule | Enforced At | Error Behavior |
|------|-------------|----------------|
| `discover_models: true` on unsupported type | `config.Load()` validation | Fail startup |
| `discover_filter` invalid glob pattern | `config.InjectDiscoveredModels()` | Fail startup |
| Provider listing API failure | `config.InjectDiscoveredModels()` | Log warning, continue |
| Discovered model conflicts with explicit entry | `config.InjectDiscoveredModels()` | Skip discovered (explicit wins) |
| Empty discovery result (0 models) | `config.InjectDiscoveredModels()` | Log info, continue normally |
