# Configuration Reference

glitchgate is configured via a YAML file, environment variables, or both.

## Config File Location

Searched in order (first found wins):

1. `--config <path>` flag
2. `~/.config/glitchgate/config.yaml`
3. `./config.yaml`
4. `/etc/glitchgate/config.yaml`

## Environment Variables

Scalar keys can be set via `GLITCHGATE_` prefixed env vars:

| Config Key      | Environment Variable       |
|-----------------|----------------------------|
| `master_key`    | `GLITCHGATE_MASTER_KEY`    |
| `listen`        | `GLITCHGATE_LISTEN`        |
| `database_path` | `GLITCHGATE_DATABASE_PATH` |
| `log_path`      | `GLITCHGATE_LOG_PATH`      |

Env vars override config file values. `providers`, `model_list`, and `oidc` require a config file.

## Full Config Reference

```yaml
# REQUIRED. Web UI password. Set here or via GLITCHGATE_MASTER_KEY.
master_key: "change-me-to-something-secure"

# Address to listen on. Default: ":4000"
listen: ":4000"

# Path to the SQLite database. Default: "glitchgate.db". Supports ~.
database_path: "~/data/glitchgate/proxy.db"

# Path to the structured JSON log file. Default: "glitchgate.log". Supports ~.
log_path: "~/data/glitchgate/proxy.log"

# IANA timezone for the web UI. Default: "UTC"
timezone: "America/New_York"

# Proxy body size limit. Default: 4 MiB.
proxy_max_body_bytes: 4194304

# Default upstream timeouts. Defaults: 2m non-streaming, 30m streaming.
upstream_request_timeout: 2m
upstream_stream_timeout: 30m

# Async request log writer settings.
async_log_buffer_size: 1000
async_log_write_timeout: 5s

# Login throttling by client IP.
login_rate_limit_per_minute: 10
login_rate_limit_burst: 5

# Proxy throttling by authenticated key plus a coarse IP guard before auth.
proxy_rate_limit_per_minute: 120
proxy_rate_limit_burst: 30
proxy_ip_rate_limit_per_minute: 240
proxy_ip_rate_limit_burst: 60

# Request log retention and stored-body truncation.
# Set request_log_retention: 0 to disable pruning.
request_log_retention: 720h
request_log_prune_interval: 1h
request_log_prune_batch_size: 1000
request_log_body_max_bytes: 65536

providers:
  - name: "anthropic"
    type: "anthropic"            # default type; can be omitted
    base_url: "https://api.anthropic.com"
    auth_mode: "proxy_key"
    api_key: "${ANTHROPIC_API_KEY}"
    default_version: "2023-06-01"

  - name: "openai-chat"
    type: "openai"
    auth_mode: "proxy_key"
    api_key: "${OPENAI_API_KEY}"

  - name: "openai-resp"
    type: "openai_responses"
    auth_mode: "proxy_key"
    api_key: "${OPENAI_API_KEY}"

  - name: "copilot"
    type: "github_copilot"
    # token_dir: "~/.config/glitchgate/copilot/"  # optional; default shown

model_list:
  - model_name: "claude-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-6"

  - model_name: "gpt-4o"
    provider: "openai-chat"
    upstream_model: "gpt-4o"
    metadata:                        # optional: override built-in pricing
      input_token_cost: 2.50         # USD per 1M tokens
      output_token_cost: 10.00
      cache_read_cost: 1.25
      cache_write_cost: 3.13

  - model_name: "claude-resilient"   # virtual model with fallback chain
    fallbacks: ["claude-sonnet", "gpt-4o"]

  - model_name: "gc/*"               # wildcard: gc/<model> → copilot
    provider: "copilot"
```

## Providers

| Field             | Required | Description |
|-------------------|----------|-------------|
| `name`            | Yes      | Unique identifier, referenced by `model_list` |
| `type`            | No       | `anthropic` (default), `github_copilot`, `openai`, `openai_responses` |
| `base_url`        | Depends  | Required for `anthropic`. Defaults to `https://api.openai.com` for `openai`/`openai_responses`. Not used by `github_copilot` |
| `auth_mode`       | Depends  | `proxy_key` or `forward`. Not used by `github_copilot` |
| `api_key`         | Depends  | For `proxy_key` mode. Supports `${ENV_VAR}` expansion |
| `default_version` | No       | Sets `anthropic-version` header when client omits it. `anthropic` only |
| `token_dir`       | No       | Token storage for `github_copilot`. Default: `~/.config/glitchgate/copilot/` |
| `stream`          | No       | `false` forces non-streaming upstream even when client requests streaming (proxy synthesizes SSE). Omit to follow client preference |

### Auth Modes

**`proxy_key`** — The proxy uses its own upstream API key. Clients authenticate with a proxy-issued key.

**`forward`** — The client's credentials are forwarded upstream. No `api_key` needed on the provider.

```yaml
providers:
  # Forward client's own key (e.g. Claude Max subscribers, Codex)
  - name: "claude-max"
    type: "anthropic"
    base_url: "https://api.anthropic.com"
    auth_mode: "forward"
```

### OpenAI Provider

| Type | Endpoint | Use when |
|------|----------|----------|
| `openai` | `/v1/chat/completions` | Standard Chat Completions API |
| `openai_responses` | `/v1/responses` | Responses API (stateful, tool-native) |

Both default `base_url` to `https://api.openai.com`, making them compatible with any OpenAI-compatible upstream (Azure, local inference, etc.).

### Format-Aware Routing

Any client format can route to any upstream provider type — the proxy handles all translation:

| Client → Upstream    | Anthropic   | OpenAI Chat  | OpenAI Responses |
|----------------------|-------------|--------------|-----------------|
| **Anthropic**        | Passthrough | Translate    | Translate        |
| **Chat Completions** | Translate   | Passthrough  | Translate        |
| **Responses API**    | Translate   | Translate    | Passthrough      |

### GitHub Copilot Provider

#### Setup

```sh
# Step 1: authenticate (one-time browser OAuth flow)
glitchgate auth copilot

# Step 2: configure
providers:
  - name: "copilot"
    type: "github_copilot"

# Step 3: add model mappings
model_list:
  - model_name: "gc/*"
    provider: "copilot"
```

Clients request models as `gc/<model-name>` (e.g. `gc/claude-sonnet-4.6`). The proxy strips the prefix.

#### Multiple Copilot Accounts

When configuring multiple Copilot providers, each must have a unique `token_dir`:

```yaml
providers:
  - name: copilot-work
    type: github_copilot
    token_dir: ~/.config/glitchgate/copilot/work/
  - name: copilot-personal
    type: github_copilot
    token_dir: ~/.config/glitchgate/copilot/personal/

model_list:
  - model_name: "work/*"
    provider: copilot-work
  - model_name: "personal/*"
    provider: copilot-personal
```

```bash
glitchgate auth copilot --name copilot-work
glitchgate auth copilot --name copilot-personal
# --force to re-authenticate an existing account
```

#### Token Storage

Tokens are stored in `token_dir` with `0600` permissions. The short-lived Copilot session token is refreshed automatically.

## Security and Operational Controls

| Field | Default | Description |
|-------|---------|-------------|
| `proxy_max_body_bytes` | `4194304` | Reject proxy request bodies above this size with `413` |
| `upstream_request_timeout` | `2m` | Default deadline for non-streaming upstream requests when the caller did not supply one |
| `upstream_stream_timeout` | `30m` | Default deadline for streaming upstream requests when the caller did not supply one |
| `async_log_buffer_size` | `1000` | Buffered request-log queue depth before backpressure and drops |
| `async_log_write_timeout` | `5s` | Maximum time allowed for one async log database insert |
| `login_rate_limit_per_minute` | `10` | Per-IP token refill rate for `POST /ui/api/login` |
| `login_rate_limit_burst` | `5` | Per-IP login burst size |
| `proxy_rate_limit_per_minute` | `120` | Per-proxy-key token refill rate for `/v1/*` |
| `proxy_rate_limit_burst` | `30` | Per-proxy-key burst size |
| `proxy_ip_rate_limit_per_minute` | `240` | Coarse per-IP refill rate on `/v1/*` before authentication |
| `proxy_ip_rate_limit_burst` | `60` | Coarse per-IP burst size on `/v1/*` before authentication |
| `request_log_retention` | `720h` | Age cutoff for pruning `request_logs`; `0` disables pruning |
| `request_log_prune_interval` | `1h` | How often the background pruning job runs |
| `request_log_prune_batch_size` | `1000` | Maximum rows deleted per prune batch |
| `request_log_body_max_bytes` | `65536` | Maximum bytes retained for stored request/response bodies after redaction |

## Model List

Maps client-facing model names to upstream providers.

### Exact Matches

```yaml
model_list:
  - model_name: "claude-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-6"
```

### Virtual Models and Fallback Chains

A model entry with `fallbacks` tries each entry in order on `5xx` or `429`:

```yaml
model_list:
  - model_name: "resilient"
    fallbacks: ["primary-sonnet", "secondary-sonnet"]
  - model_name: "primary-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-6"
  - model_name: "secondary-sonnet"
    provider: "anthropic-backup"
    upstream_model: "claude-sonnet-4-6"
```

| Upstream response    | Behavior |
|----------------------|----------|
| `5xx`                | Try next entry |
| `429`                | Try next entry |
| Other `4xx`          | Return immediately |
| Network error        | Try next; else `502` |
| All entries exhausted | `503` |

Nested virtual models are supported and flattened at startup. Circular references are rejected at startup. Each request log records `fallback_attempts`.

`fallbacks` and `provider`/`upstream_model` are mutually exclusive.

### Wildcard Routing

`model_name` ending with `/*` matches any request with that prefix. The suffix becomes the upstream model name:

```yaml
model_list:
  - model_name: "claude_max/*"
    provider: "claude-max"
  # Exact overrides wildcard:
  - model_name: "claude_max/claude-sonnet-4-6"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-6"
```

Resolution order: exact match → first wildcard match → error.

### Per-Model Pricing Override

Override built-in pricing for any concrete model entry with `metadata`:

```yaml
model_list:
  - model_name: "my-model"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-6"
    metadata:
      input_token_cost: 3.00    # USD per 1M input tokens
      output_token_cost: 15.00  # USD per 1M output tokens
      cache_read_cost: 0.30     # USD per 1M cache read tokens
      cache_write_cost: 3.75    # USD per 1M cache write tokens
```

## Built-in Pricing

Default pricing is applied automatically based on provider type and `base_url`. No configuration needed for official API endpoints.

### Anthropic (official `api.anthropic.com`)

| Model                      | Input  | Output  | Cache Write | Cache Read |
|----------------------------|-------:|--------:|------------:|-----------:|
| `claude-opus-4-6`          | $5.00  | $25.00  | $6.25       | $0.50      |
| `claude-sonnet-4-6`        | $3.00  | $15.00  | $3.75       | $0.30      |
| `claude-haiku-4-5`         | $1.00  | $5.00   | $1.25       | $0.10      |
| `claude-opus-4-20250514`   | $15.00 | $75.00  | $18.75      | $1.50      |
| `claude-sonnet-4-20250514` | $3.00  | $15.00  | $3.75       | $0.30      |
| `claude-haiku-4-20250514`  | $0.80  | $4.00   | $1.00       | $0.08      |

### OpenAI (official `api.openai.com`)

| Model         | Input  | Output  | Cache Read |
|---------------|-------:|--------:|-----------:|
| `gpt-4o`      | $2.50  | $10.00  | $1.25      |
| `gpt-4o-mini` | $0.15  | $0.60   | $0.075     |
| `gpt-4.1`     | $2.00  | $8.00   | $0.50      |
| `gpt-4.1-mini`| $0.40  | $1.60   | $0.10      |
| `gpt-4.1-nano`| $0.10  | $0.40   | $0.025     |
| `o3`          | $2.00  | $8.00   | —          |
| `o4-mini`     | $1.10  | $4.40   | —          |

### GitHub Copilot

All Copilot models are $0 (subscription-billed). Override with `metadata` to track notional costs.

## OIDC Authentication

```yaml
oidc:
  issuer_url: "https://accounts.example.com"
  client_id: "your-client-id"
  client_secret: "${OIDC_CLIENT_SECRET}"
  redirect_url: "https://proxy.example.com/ui/auth/callback"
  scopes: ["openid", "email", "profile"]  # default; rarely needs changing
```

Register the redirect URI `https://your-proxy-host/ui/auth/callback` with your IDP. The IDP must return at minimum the `email` claim.

### Roles

| Role           | Capabilities |
|----------------|-------------|
| `global_admin` | Full access: users, teams, all keys, all logs/costs |
| `team_admin`   | Own team members; team-scoped logs, costs, keys |
| `member`       | Own keys, logs, costs only |

The first OIDC user is automatically `global_admin`.

### Break-Glass

When OIDC is configured, the master key login is hidden. Access it at `/ui/login?master=1`.

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
  - model_name: "claude-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-6"
```

### Anthropic + OpenAI Fallback

```yaml
master_key: "change-me"

providers:
  - name: "anthropic"
    base_url: "https://api.anthropic.com"
    auth_mode: "proxy_key"
    api_key: "${ANTHROPIC_API_KEY}"
    default_version: "2023-06-01"
  - name: "openai"
    type: "openai"
    auth_mode: "proxy_key"
    api_key: "${OPENAI_API_KEY}"

model_list:
  - model_name: "claude-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-6"
  - model_name: "gpt-4o"
    provider: "openai"
    upstream_model: "gpt-4o"
  - model_name: "resilient"
    fallbacks: ["claude-sonnet", "gpt-4o"]
```

### Anthropic + GitHub Copilot

```yaml
master_key: "change-me"

providers:
  - name: "anthropic"
    base_url: "https://api.anthropic.com"
    auth_mode: "proxy_key"
    api_key: "${ANTHROPIC_API_KEY}"
    default_version: "2023-06-01"
  - name: "copilot"
    type: "github_copilot"

model_list:
  - model_name: "claude-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-6"
  - model_name: "gc/*"
    provider: "copilot"
```

### Environment-Only (no config file)

```bash
export GLITCHGATE_MASTER_KEY="change-me"
export GLITCHGATE_LISTEN=":8080"
export GLITCHGATE_DATABASE_PATH="/var/lib/glitchgate/data.db"
```

`providers`, `model_list`, and `oidc` require a config file.
