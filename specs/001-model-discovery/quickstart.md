# Quickstart: Automatic Model Discovery

**Feature**: 001-model-discovery | **Date**: 2026-03-20

## Configuration

Add `discover_models: true` and optionally `model_prefix` / `discover_filter` to any supported provider in `config.yaml`:

```yaml
providers:
  - name: anthropic
    type: anthropic
    auth_mode: proxy_key
    api_key: "sk-ant-..."
    discover_models: true
    # model_prefix: "anthropic/"    # default: "{name}/" → "anthropic/"
    # discover_filter:              # default: include all
    #   - "claude-*"                # only claude models
    #   - "!*-preview"              # exclude preview models

  - name: openai
    type: openai
    auth_mode: proxy_key
    api_key: "sk-..."
    base_url: "https://api.openai.com"
    discover_models: true
    model_prefix: ""                # no prefix: model names match upstream exactly

  - name: gemini
    type: gemini
    auth_mode: api_key
    api_key: "AIza..."
    discover_models: true
    discover_filter:
      - "gemini-*"                  # only gemini models, not embedding/etc.

model_list:
  # Explicit entries always take precedence over discovered ones
  - model_name: anthropic/claude-sonnet-4-6
    provider: anthropic
    upstream_model: claude-sonnet-4-6
    metadata:
      input_token_cost: 3.0
      output_token_cost: 15.0
```

## Behavior

1. **Startup**: Server calls each provider's model listing API
2. **Naming**: Discovered models get names like `{model_prefix}{upstream_id}` (e.g., `anthropic/claude-opus-4-6`)
3. **Precedence**: If `anthropic/claude-sonnet-4-6` exists in explicit `model_list`, the explicit entry wins (with its pricing metadata)
4. **Failure**: If a provider's listing API is unreachable, the server logs a warning and starts without those discovered models
5. **Unsupported**: Setting `discover_models: true` on `github_copilot` is a config error — server refuses to start

## Verify

```bash
# Start the server
./glitchgate serve

# Check logs for discovery output
# Expected: "discovered N models from provider X"

# List available models (hit any configured endpoint)
curl -s http://localhost:4000/anthropic/v1/messages \
  -H "Authorization: Bearer $PROXY_KEY" \
  -d '{"model": "anthropic/claude-opus-4-6", "messages": [{"role": "user", "content": "hi"}], "max_tokens": 10}'
```

## Filter Examples

| Config | Effect |
|--------|--------|
| `discover_filter: ["claude-*"]` | Only models starting with `claude-` |
| `discover_filter: ["*", "!*-preview"]` | All models except preview |
| `discover_filter: ["gemini-2*", "gemini-1.5*"]` | Only Gemini 2.x and 1.5.x models |
| (absent) | All discovered models included |
