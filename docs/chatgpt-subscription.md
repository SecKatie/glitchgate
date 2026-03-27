# Using glitchgate with a ChatGPT Subscription

This guide explains how to route OpenAI Codex CLI requests through glitchgate using your ChatGPT Pro (or Plus) subscription, giving you request logging, cost tracking, and model fallback support for Codex usage.

## How It Works

When you sign into Codex with your ChatGPT account, it authenticates via OAuth and sends requests to `chatgpt.com/backend-api/codex/v1/responses` (not `api.openai.com`). glitchgate sits between Codex and this backend, forwarding your OAuth credentials while logging every request.

```
Codex CLI
  → glitchgate (localhost:4000)
    → chatgpt.com/backend-api/codex/v1/responses
```

The proxy authenticates Codex using a proxy API key (via the `X-Proxy-Api-Key` header), then forwards your ChatGPT OAuth token in the `Authorization` header to the upstream.

## Prerequisites

- glitchgate running locally
- A glitchgate proxy API key (create one via the web UI or `glitchgate keys create`)
- Codex CLI installed and signed in with your ChatGPT account (`codex login`)

## glitchgate Configuration

Add a `chatgpt-pro` provider to your glitchgate config (`~/.config/glitchgate/config.yaml`):

```yaml
providers:
  - name: "chatgpt-pro"
    type: "openai_responses"
    base_url: "https://chatgpt.com/backend-api/codex/v1"
    auth_mode: "forward"
```

Key details:

- **`type: "openai_responses"`** — Codex uses the OpenAI Responses API format.
- **`base_url`** — Points at the ChatGPT backend including `/v1`, not `api.openai.com`. glitchgate appends only the resource path (e.g. `/responses`), never `/v1`.
- **`auth_mode: "forward"`** — Forwards Codex's OAuth `Authorization` header to the upstream as-is. No `api_key` is needed on the provider.

### Model Mappings

You can use wildcard routing for flexible model access, explicit mappings for specific models, or both:

```yaml
model_list:
  # Wildcard: chatgpt/<model> routes to chatgpt-pro
  - model_name: "chatgpt/*"
    provider: "chatgpt-pro"

  # Explicit: expose gpt-5.4 without a prefix
  # (required for Codex, which validates model names client-side)
  - model_name: "gpt-5.4"
    provider: "chatgpt-pro"
    upstream_model: "gpt-5.4"
```

**Why explicit mappings?** Codex validates model names client-side when you're signed in with a ChatGPT account. Prefixed names like `chatgpt/gpt-5.4` are rejected before the request is even sent. Explicit mappings let you use the bare model name (`gpt-5.4`) so Codex accepts it, while still routing through glitchgate.

### Full Example

```yaml
master_key: "your-master-key"
listen: ":4000"
timezone: "America/New_York"
database_path: "~/.local/share/glitchgate/glitchgate.db"

providers:
  - name: "chatgpt-pro"
    type: "openai_responses"
    base_url: "https://chatgpt.com/backend-api/codex/v1"
    auth_mode: "forward"

model_list:
  - model_name: "chatgpt/*"
    provider: "chatgpt-pro"
  - model_name: "gpt-5.4"
    provider: "chatgpt-pro"
    upstream_model: "gpt-5.4"
```

## Codex Configuration

Edit `~/.codex/config.toml` to define glitchgate as a model provider and set it as the default:

```toml
model = "gpt-5.4"
model_provider = "glitchgate"

[model_providers.glitchgate]
name = "Glitchgate"
base_url = "http://localhost:4000/v1"

[model_providers.glitchgate.env_http_headers]
"X-Proxy-Api-Key" = "GLITCHGATE_API_KEY"
```

Key details:

- **`model_provider = "glitchgate"`** — Makes glitchgate the default provider. Without this, Codex routes known model names (like `gpt-5.4`) directly to OpenAI, bypassing the proxy entirely.
- **`base_url`** — Includes the `/v1` prefix because Codex appends `/responses` directly to the base URL.
- **`env_http_headers`** — Sends the `X-Proxy-Api-Key` header populated from the `GLITCHGATE_API_KEY` environment variable. This authenticates with the proxy without interfering with the `Authorization` header (which carries your ChatGPT OAuth token).

## Environment Variables

Set your glitchgate proxy API key:

```bash
export GLITCHGATE_API_KEY="llmp_sk_..."
```

Add this to your shell profile (`~/.zshrc`, `~/.bashrc`, etc.) so it persists across sessions.

No `OPENAI_API_KEY` is needed — Codex handles authentication with the ChatGPT backend via its own OAuth flow.

## Usage

```bash
# Uses the default model (gpt-5.4) through glitchgate
codex "your prompt"

# Explicitly specify the provider
codex --provider glitchgate --model "gpt-5.4" "your prompt"
```

## Verifying It Works

1. Make a request with Codex
2. Open the glitchgate web UI at `http://localhost:4000/ui`
3. Check the request logs — you should see entries with:
   - **Source Format**: `responses`
   - **Provider**: `chatgpt-pro`
   - **Model Requested**: `gpt-5.4`
   - **Model Upstream**: `gpt-5.4`

## Troubleshooting

### "Missing proxy API key" (401)

Codex isn't sending the `X-Proxy-Api-Key` header. Verify:
- `GLITCHGATE_API_KEY` is set in your environment
- The `env_http_headers` section is present in `~/.codex/config.toml`

### "Unknown model" (400)

The model name doesn't match any entry in `model_list`. Either add an explicit mapping or use the `chatgpt/*` wildcard prefix.

### "model is not supported when using Codex with a ChatGPT account"

This is Codex client-side validation rejecting the model name. Use bare model names (e.g., `gpt-5.4`) with explicit mappings instead of prefixed names (e.g., `chatgpt/gpt-5.4`).

### Stream disconnects before completion

Check that `base_url` in the glitchgate provider points to `https://chatgpt.com/backend-api/codex/v1` (not `https://api.openai.com`). ChatGPT OAuth tokens are only valid for the ChatGPT backend.

### No logs appearing in glitchgate

Codex is bypassing the proxy. Make sure `model_provider = "glitchgate"` is set in `~/.codex/config.toml` — without it, Codex uses its built-in provider for recognized model names.
