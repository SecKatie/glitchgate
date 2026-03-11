# Quickstart: LLM Proxy

## Prerequisites

- Go 1.24+ installed
- An Anthropic API key (or a Claude Max subscription)

## Build

```bash
git clone codeberg.org/kglitchy/llm-proxy
cd llm-proxy
make build
```

This produces a single static binary at `./llm-proxy`.

## Configure

Create a configuration file at `~/.config/llm-proxy/config.yaml`:

### Option A: Using a standard Anthropic API key

```yaml
master_key: "your-master-key-for-web-ui"
listen: ":4000"

providers:
  - name: "anthropic"
    base_url: "https://api.anthropic.com"
    auth_mode: "proxy_key"
    api_key: "${ANTHROPIC_API_KEY}"
    default_version: "2023-06-01"

model_list:
  - model_name: "claude-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-20250514"
  - model_name: "claude-opus"
    provider: "anthropic"
    upstream_model: "claude-opus-4-20250514"
```

### Option B: Using Claude Max subscription (credential forwarding)

```yaml
master_key: "your-master-key-for-web-ui"
listen: ":4000"

providers:
  - name: "claude-max"
    base_url: "https://api.anthropic.com"
    auth_mode: "forward"
    default_version: "2023-06-01"

model_list:
  - model_name: "claude-sonnet-4-20250514"
    provider: "claude-max"
    upstream_model: "claude-sonnet-4-20250514"
  - model_name: "claude-opus-4-20250514"
    provider: "claude-max"
    upstream_model: "claude-opus-4-20250514"
```

### Option C: Both (switch between them by model name)

```yaml
master_key: "your-master-key-for-web-ui"
listen: ":4000"

providers:
  - name: "anthropic"
    base_url: "https://api.anthropic.com"
    auth_mode: "proxy_key"
    api_key: "${ANTHROPIC_API_KEY}"
    default_version: "2023-06-01"

  - name: "claude-max"
    base_url: "https://api.anthropic.com"
    auth_mode: "forward"
    default_version: "2023-06-01"

model_list:
  - model_name: "claude-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-20250514"
  - model_name: "claude-sonnet-max"
    provider: "claude-max"
    upstream_model: "claude-sonnet-4-20250514"
```

## Create a Proxy API Key

```bash
llm-proxy keys create --label "claude-code"
```

Output:

```
Created API key: llmp_sk_abc123...xyz789
Label: claude-code
Prefix: llmp_sk_

Save this key now — it will not be shown again.
```

## Start the Proxy

```bash
llm-proxy serve
```

The proxy starts on the configured port (default `:4000`).

## Point Claude Code at the Proxy

### With standard API key (proxy-owned)

```bash
export ANTHROPIC_BASE_URL=http://localhost:4000
export ANTHROPIC_API_KEY=llmp_sk_abc123...xyz789  # your proxy key
```

### With Claude Max subscription (credential forwarding)

```bash
export ANTHROPIC_BASE_URL=http://localhost:4000
export ANTHROPIC_MODEL=claude-sonnet-max  # routes to claude-max provider
# Your OAuth token is forwarded automatically via Authorization header
# The proxy API key goes in a custom header:
export ANTHROPIC_CUSTOM_HEADERS="x-proxy-api-key: llmp_sk_abc123...xyz789"
```

Now run Claude Code normally — all requests are proxied and logged.

## View Logs and Costs

Open your browser to `http://localhost:4000/ui`.

1. Log in with your master key.
2. **Logs** tab: browse, filter, and inspect all proxied requests.
3. **Costs** tab: view total spend by model, by API key, and over time.

## CLI Reference

```bash
llm-proxy serve                  # start the proxy server
llm-proxy keys create --label X  # create a new proxy API key
llm-proxy keys list              # list all API keys (prefix + label)
llm-proxy keys revoke <prefix>   # revoke an API key
llm-proxy --help                 # full help
```
