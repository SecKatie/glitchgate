# Configuration Reference

llm-proxy is configured via a YAML file, environment variables, or a combination of both.

## Config File Location

The config file is searched in the following order:

1. Path passed via `--config` flag (e.g. `llm-proxy --config /path/to/config.yaml serve`)
2. `~/.config/llm-proxy/config.yaml`
3. `./config.yaml` (current working directory)
4. `/etc/llm-proxy/config.yaml`

The first file found wins. If no file is found, llm-proxy falls back to defaults and environment variables.

## Environment Variables

All config keys can be set via environment variables with the prefix `LLM_PROXY_`. Dots and nested keys use underscores:

| Config Key      | Environment Variable        |
|-----------------|-----------------------------|
| `master_key`    | `LLM_PROXY_MASTER_KEY`      |
| `listen`        | `LLM_PROXY_LISTEN`          |
| `database_path` | `LLM_PROXY_DATABASE_PATH`   |

Environment variables override config file values.

## Full Config Reference

```yaml
# REQUIRED. Password for the web UI. Set here or via LLM_PROXY_MASTER_KEY.
master_key: "change-me-to-something-secure"

# Address to listen on. Default: ":4000"
listen: ":4000"

# Path to the SQLite database file. Default: "llm-proxy.db"
# Supports ~ for home directory. Parent directories are created automatically.
database_path: "~/data/llm-proxy/proxy.db"

# Upstream LLM providers.
providers:
  - name: "anthropic"               # Unique name, referenced by model_list
    type: "anthropic"               # Provider type. Default: "anthropic"
    base_url: "https://api.anthropic.com"
    auth_mode: "proxy_key"           # "proxy_key" or "forward" (see below)
    api_key: "${ANTHROPIC_API_KEY}"  # Supports $ENV_VAR expansion
    default_version: "2023-06-01"    # Anthropic-Version header

# Maps client-facing model names to upstream provider + model.
model_list:
  - model_name: "claude-sonnet"              # Name clients send in requests
    provider: "anthropic"                    # Must match a provider name above
    upstream_model: "claude-sonnet-4-20250514" # Actual model sent upstream

# Optional: override or add to the built-in pricing table.
pricing:
  - model: "claude-sonnet-4-20250514"
    input_per_million: 3.00    # USD per 1M input tokens
    output_per_million: 15.00  # USD per 1M output tokens
```

## Providers

Each provider entry configures an upstream LLM API endpoint.

### Fields

| Field             | Required | Description |
|-------------------|----------|-------------|
| `name`            | Yes      | Unique identifier, referenced by `model_list` entries |
| `type`            | No       | Provider type: `"anthropic"` (default). Determines how requests are formatted and sent upstream |
| `base_url`        | Yes      | Upstream API base URL (e.g. `https://api.anthropic.com`) |
| `auth_mode`       | Yes      | How the proxy authenticates with the upstream (see below) |
| `api_key`         | Depends  | API key for `proxy_key` mode. Supports `${ENV_VAR}` expansion |
| `default_version` | No       | Sets the `anthropic-version` header if the client doesn't send one |

### Auth Modes

**`proxy_key`** — The proxy owns the upstream API key. Clients authenticate with a proxy-issued key (`x-api-key` header), and the proxy substitutes its own key when forwarding upstream.

```yaml
providers:
  - name: "anthropic"
    type: "anthropic"
    base_url: "https://api.anthropic.com"
    auth_mode: "proxy_key"
    api_key: "${ANTHROPIC_API_KEY}"
```

**`forward`** — The client's own credentials (e.g. OAuth token from Claude Max) are forwarded to the upstream as-is. The proxy API key is sent in the `x-api-key` header for proxy authentication only.

```yaml
providers:
  - name: "claude-max"
    type: "anthropic"
    base_url: "https://api.anthropic.com"
    auth_mode: "forward"
```

## Model List

Maps client-facing model names to upstream providers. When a client sends `"model": "claude-sonnet"`, the proxy looks it up here, routes to the matched provider, and rewrites the model to the upstream name.

### Exact Matches

```yaml
model_list:
  - model_name: "claude-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-20250514"
  - model_name: "claude-opus"
    provider: "anthropic"
    upstream_model: "claude-opus-4-20250514"
```

You can map the same upstream model to multiple client-facing names routed through different providers:

```yaml
model_list:
  - model_name: "claude-sonnet"        # Uses API key
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-20250514"
  - model_name: "claude-sonnet-max"    # Forwards client credentials
    provider: "claude-max"
    upstream_model: "claude-sonnet-4-20250514"
```

### Wildcard Model Routing

A model entry whose `model_name` ends with `/*` is a **wildcard**. Any client request whose model starts with the prefix (everything before `/*`) followed by `/` will match. The proxy strips the prefix and uses the remainder as the upstream model name. The `upstream_model` field is ignored for wildcard entries.

```yaml
model_list:
  - model_name: "claude_max/*"
    provider: "claude-max"
    # upstream_model is not needed — derived from the client request
```

With this entry, a request for `claude_max/claude-sonnet-4-20250514` routes to the `claude-max` provider with upstream model `claude-sonnet-4-20250514`.

#### Precedence Rules

Model resolution follows this order:

1. **Exact match** — If a `model_name` matches the client model exactly, it wins regardless of any wildcards.
2. **Wildcard match** — If no exact match is found, the first wildcard entry (in config order) whose prefix matches is used.
3. **Error** — If neither matches, the request is rejected.

This means you can override specific models under a wildcard prefix:

```yaml
model_list:
  # Exact match — this specific model uses the API key provider
  - model_name: "claude_max/claude-sonnet-4-20250514"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-20250514"

  # Wildcard — everything else under claude_max/ forwards credentials
  - model_name: "claude_max/*"
    provider: "claude-max"
```

| Client sends                          | Match type | Provider    | Upstream model              |
|---------------------------------------|------------|-------------|-----------------------------|
| `claude_max/claude-sonnet-4-20250514` | Exact      | anthropic   | `claude-sonnet-4-20250514`  |
| `claude_max/claude-opus-4-20250514`   | Wildcard   | claude-max  | `claude-opus-4-20250514`    |
| `claude_max/`                         | Error      | —           | Empty suffix is invalid     |
| `unknown-model`                       | Error      | —           | No match found              |

#### Logging

Wildcard-routed requests log both names:

- **Model Requested** — the full client name (e.g. `claude_max/claude-sonnet-4-20250514`)
- **Model Upstream** — the stripped suffix sent to the provider (e.g. `claude-sonnet-4-20250514`)

Cost calculation uses the upstream model name for pricing lookups.

## Pricing

Built-in pricing for common models is included:

| Model                        | Input ($/1M tokens) | Output ($/1M tokens) |
|------------------------------|--------------------:|---------------------:|
| `claude-sonnet-4-20250514`   | $3.00               | $15.00               |
| `claude-opus-4-20250514`     | $15.00              | $75.00               |
| `claude-haiku-4-20250514`    | $0.80               | $4.00                |

Override or add models with the `pricing` section:

```yaml
pricing:
  - model: "claude-sonnet-4-20250514"
    input_per_million: 3.00
    output_per_million: 15.00
  - model: "my-custom-model"
    input_per_million: 1.00
    output_per_million: 5.00
```

Config pricing entries merge with (and override) the built-in defaults.

## Example Configs

### Minimal

```yaml
master_key: "change-me"

providers:
  - name: "anthropic"
    base_url: "https://api.anthropic.com"
    auth_mode: "proxy_key"
    api_key: "${ANTHROPIC_API_KEY}"
    default_version: "2023-06-01"

model_list:
  - model_name: "claude-sonnet-4-20250514"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-20250514"
```

### Environment-only (no config file)

```bash
export LLM_PROXY_MASTER_KEY="change-me"
export LLM_PROXY_LISTEN=":8080"
export LLM_PROXY_DATABASE_PATH="/var/lib/llm-proxy/data.db"
```

Note: providers and model_list cannot be set via environment variables — they require a config file.

### Multiple providers with wildcard routing

```yaml
master_key: "change-me"
listen: ":4000"
database_path: "/var/lib/llm-proxy/proxy.db"

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
  # Exact matches — use the API key provider
  - model_name: "claude-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-20250514"
  - model_name: "claude-opus"
    provider: "anthropic"
    upstream_model: "claude-opus-4-20250514"

  # Wildcard — any claude_max/<model> forwards client credentials
  - model_name: "claude_max/*"
    provider: "claude-max"
```
