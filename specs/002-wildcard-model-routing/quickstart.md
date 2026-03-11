# Quickstart: Wildcard Model Routing

## Configure a wildcard model entry

Add a wildcard pattern to `model_list` in your config file. The `/*` suffix tells the proxy to match any model name with that prefix:

```yaml
master_key: "your-master-key"

providers:
  - name: "anthropic"
    type: "anthropic"
    base_url: "https://api.anthropic.com"
    auth_mode: "proxy_key"
    api_key: "${ANTHROPIC_API_KEY}"
    default_version: "2023-06-01"

  - name: "claude-max"
    type: "anthropic"
    base_url: "https://api.anthropic.com"
    auth_mode: "forward"
    default_version: "2023-06-01"

model_list:
  # Exact match — this specific model uses the API key provider
  - model_name: "claude-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-20250514"

  # Wildcard — anything starting with "claude_max/" forwards credentials
  - model_name: "claude_max/*"
    provider: "claude-max"
```

## Send a request using a wildcard model

The proxy strips the `claude_max/` prefix and sends the remainder as the upstream model:

```bash
curl http://localhost:4000/v1/messages \
  -H "x-api-key: $PROXY_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude_max/claude-sonnet-4-20250514",
    "messages": [{"role": "user", "content": "Hello"}],
    "max_tokens": 100
  }'
```

The proxy routes this to the "claude-max" provider with `model: "claude-sonnet-4-20250514"`.

## Combine with exact matches

Exact model entries always take priority over wildcards:

```yaml
model_list:
  # This exact match wins for claude-sonnet-4-20250514
  - model_name: "claude_max/claude-sonnet-4-20250514"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-20250514"

  # Wildcard catches everything else
  - model_name: "claude_max/*"
    provider: "claude-max"
```

With this config:
- `claude_max/claude-sonnet-4-20250514` → routed to "anthropic" (exact match)
- `claude_max/claude-opus-4-20250514` → routed to "claude-max" (wildcard)

## Verify in the web UI

Open `http://localhost:4000/ui/logs` and check:
- **Model Requested** shows the full client model name (e.g., `claude_max/claude-sonnet-4-20250514`)
- **Model Upstream** shows the stripped model name sent to the provider (e.g., `claude-sonnet-4-20250514`)
