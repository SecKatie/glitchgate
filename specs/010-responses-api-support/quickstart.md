# Quickstart: OpenAI Responses API Support

**Feature**: 010-responses-api-support

## What This Feature Does

Adds OpenAI as a provider (Chat Completions + Responses API) and makes the Responses API a first-class proxy format. After this feature, the proxy supports a 3×3 translation matrix:

| Client Format → | Anthropic Upstream | Chat Completions Upstream | Responses API Upstream |
|----------------|-------------------|--------------------------|----------------------|
| Anthropic | Passthrough | Translate | Translate (NEW) |
| Chat Completions | Translate | Passthrough | Translate (NEW) |
| Responses API (NEW) | Translate (NEW) | Translate (NEW) | Passthrough (NEW) |

## Key Files to Understand

Before working on this feature, read these files in order:

1. **`internal/provider/provider.go`** — The provider interface. New OpenAI provider implements this.
2. **`internal/translate/openai_types.go`** — Existing OpenAI type definitions. Responses API types follow the same pattern.
3. **`internal/translate/openai_to_anthropic.go`** — Example translation function. New translations follow this pattern.
4. **`internal/proxy/handler.go`** — Anthropic input handler with format-aware routing. Shows how `APIFormat()` drives dispatch.
5. **`internal/proxy/openai_handler.go`** — OpenAI input handler. The new Responses handler follows this pattern.
6. **`internal/proxy/stream.go`** — SSE streaming relay. New streaming functions go here.

## Configuration

Add an OpenAI provider to config:

```yaml
providers:
  - name: openai-chat
    type: openai
    base_url: https://api.openai.com
    auth_mode: proxy_key
    api_key: sk-...

  - name: openai-responses
    type: openai_responses
    base_url: https://api.openai.com
    auth_mode: proxy_key
    api_key: sk-...

models:
  - model_name: gpt-4o
    provider: openai-chat
    upstream_model: gpt-4o

  - model_name: gpt-4o-responses
    provider: openai-responses
    upstream_model: gpt-4o
```

## Testing

```bash
make test           # Run all tests including new translation tests
make lint           # golangci-lint (must pass with zero warnings)
make audit          # gosec + govulncheck
```

Test patterns to follow:
- Table-driven tests with `testify/require`
- Contract tests validating translation against published API schemas
- Benchmark tests for translation hot paths (per constitution)

## Architecture Decisions

- **`APIFormat()` returns `"responses"`** for Responses API providers (distinct from `"openai"` for Chat Completions)
- **Translation functions are pure** — no HTTP framework types, independently testable
- **Streaming is incremental** — never buffer full responses (constitution requirement)
- **Essential vs optional**: Content-bearing features error if untranslatable; behavioral parameters silently dropped
- **Multimodal**: Images translated across formats; files/audio error when target format lacks support
