# Configuration Reference

glitchgate is configured via a YAML file, environment variables, or both.

## Config File Location

Searched in order (first found wins):

1. `--config <path>` flag
2. `~/.config/glitchgate/config.yaml`
3. `./config.yaml`
4. `/etc/glitchgate/config.yaml`

## Quick Examples

### Minimal (Single Provider)

```yaml
master_key: "change-me-to-something-secure"

providers:
  - name: "anthropic"
    base_url: "https://api.anthropic.com/v1"
    auth_mode: "proxy_key"
    api_key: "${ANTHROPIC_API_KEY}"
    default_version: "2023-06-01"

model_list:
  - model_name: "claude-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-6"
```

### Fallback Chain (Resilience)

```yaml
providers:
  - name: "anthropic"
    base_url: "https://api.anthropic.com/v1"
    auth_mode: "proxy_key"
    api_key: "${ANTHROPIC_API_KEY}"
  - name: "openai"
    type: "openai"
    auth_mode: "proxy_key"
    api_key: "${OPENAI_API_KEY}"

model_list:
  - model_name: "resilient"
    fallbacks: ["claude-sonnet", "gpt-4o"]
  - model_name: "claude-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-6"
  - model_name: "gpt-4o"
    provider: "openai"
    upstream_model: "gpt-4o"
```

### PostgreSQL (Multi-Instance)

```yaml
master_key: "change-me"

# Use database_url instead of database_path for PostgreSQL
database_url: "postgres://user:pass@localhost/glitchgate?sslmode=require"

providers:
  - name: "anthropic"
    # ...
```

## Environment Variables

Scalar keys can be set via `GLITCHGATE_` prefixed env vars:

| Config Key      | Environment Variable       |
|-----------------|----------------------------|
| `master_key`    | `GLITCHGATE_MASTER_KEY`    |
| `listen`        | `GLITCHGATE_LISTEN`        |
| `database_path` | `GLITCHGATE_DATABASE_PATH` |
| `log_path`      | `GLITCHGATE_LOG_PATH`      |

Env vars override config file values. `providers`, `model_list`, and `oidc` require a config file.

---

## Common Patterns

### Single Provider Setup

The simplest configuration. All requests route to one upstream provider.

```yaml
master_key: "secure-password"

providers:
  - name: "anthropic"
    base_url: "https://api.anthropic.com/v1"
    auth_mode: "proxy_key"
    api_key: "${ANTHROPIC_API_KEY}"
    default_version: "2023-06-01"

model_list:
  - model_name: "claude"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-6"
```

### Multiple Copilot Accounts

Separate work and personal Copilot subscriptions with prefix routing:

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

Authenticate each:
```bash
glitchgate auth copilot --name copilot-work
glitchgate auth copilot --name copilot-personal
```

---

| Field             | Required | Description |
|-------------------|----------|-------------|
| `name`            | Yes      | Unique identifier, referenced by `model_list` |
| `type`            | No       | `anthropic` (default), `github_copilot`, `openai`, `openai_responses`, `gemini` |
| `base_url`        | No       | Full base URL including path prefix (e.g. `https://api.openai.com/v1`). glitchgate appends only the resource path (e.g. `/chat/completions`), never `/v1`. Defaults to `https://api.anthropic.com/v1` for `anthropic`, `https://api.openai.com/v1` for `openai`/`openai_responses`. Not used by `github_copilot` or `gemini` |
| `auth_mode`       | Depends  | `proxy_key` or `forward` for most providers. `anthropic` and `gemini` also support `vertex`. For `gemini`: `api_key` or `vertex`. Not used by `github_copilot` |
| `api_key`         | Depends  | For `proxy_key`/`api_key` mode. Supports `${ENV_VAR}` expansion |
| `default_version` | No       | Sets `anthropic-version` header when client omits it. `anthropic` only |
| `token_dir`       | No       | Token storage for `github_copilot`. Default: `~/.config/glitchgate/copilot/` |
| `credentials_file`| No       | Path to GCP service account JSON for `anthropic` or `gemini` (vertex mode). Omit to use Application Default Credentials (ADC). Supports `~` and `${ENV_VAR}` |
| `project`         | Depends  | GCP project ID. Required for `anthropic` and `gemini` in vertex mode |
| `region`          | Depends  | GCP region (e.g. `us-east5`). Required for `anthropic` (vertex mode). Defaults to `us-central1` for `gemini` (vertex mode) |
| `stream`          | No       | `false` forces non-streaming upstream even when client requests streaming (proxy synthesizes SSE). Omit to follow client preference |
| `monthly_subscription_cost` | No | Optional monthly provider subscription cost in USD, used by the cost dashboard to compare flat subscription spend against token-based usage |

### Auth Modes

**`proxy_key`** — The proxy uses its own upstream API key. Clients authenticate with a proxy-issued key.

**`forward`** — The client's credentials are forwarded upstream. No `api_key` needed on the provider.

```yaml
providers:
  # Forward client's own key (e.g. Claude Max subscribers, Codex)
  - name: "claude-max"
    type: "anthropic"
    base_url: "https://api.anthropic.com/v1"
    auth_mode: "forward"

  # Or forward the client's Gemini Developer API key.
  - name: "gemini-forward"
    type: "gemini"
    auth_mode: "forward"
```

### OpenAI Provider

| Type | Endpoint | Use when |
|------|----------|----------|
| `openai` | `/openai/v1/chat/completions` | Standard Chat Completions API |
| `openai_responses` | `/openai/v1/responses` | Responses API (stateful, tool-native) |

Both default `base_url` to `https://api.openai.com/v1`, making them compatible with any OpenAI-compatible upstream (Azure, local inference, etc.).

### Format-Aware Routing

Any client format can route to any upstream provider type — the proxy handles all translation:

| Client → Upstream    | Anthropic   | OpenAI Chat  | OpenAI Responses | Native Gemini |
|----------------------|-------------|--------------|-----------------|---------------|
| **Anthropic**        | Passthrough | Translate    | Translate        | Translate     |
| **Chat Completions** | Translate   | Passthrough  | Translate        | Translate     |
| **Responses API**    | Translate   | Translate    | Passthrough      | Translate     |

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

---

### Anthropic via Vertex AI

Routes Claude model requests through Google Cloud Vertex AI. Authentication uses GCP OAuth2 credentials (service account JSON or Application Default Credentials). The proxy manages token refresh automatically.

#### Setup

```yaml
providers:
  - name: "vertex-claude"
    type: "anthropic"
    auth_mode: "vertex"
    project: "my-gcp-project"
    region: "us-east5"
    # credentials_file: "/path/to/service-account.json"  # optional; omit for ADC

model_list:
  - model_name: "vertex-sonnet"
    provider: "vertex-claude"
    upstream_model: "claude-sonnet-4-6-20250514"
  - model_name: "vertex/*"         # wildcard: vertex/<model> → vertex-claude
    provider: "vertex-claude"
```

#### Authentication

**Application Default Credentials (ADC)** — If `credentials_file` is omitted, the provider uses ADC, which checks (in order):

1. `GOOGLE_APPLICATION_CREDENTIALS` environment variable
2. `~/.config/gcloud/application_default_credentials.json` (from `gcloud auth application-default login`)
3. GCE/GKE metadata server (when running on Google Cloud)

**Service account JSON** — Set `credentials_file` to the path of a service account key file. The service account needs the `Vertex AI User` role (`roles/aiplatform.user`).

#### Pricing

Built-in Anthropic pricing is applied automatically. Override with `metadata` on individual model entries if Vertex pricing differs.

#### Notes

- The proxy constructs Vertex AI URLs from `project` and `region` — no `base_url` needed
- Streaming uses Vertex's `streamRawPredict` endpoint; non-streaming uses `rawPredict`
- The `model` field is stripped from the request body (Vertex expects it in the URL path)
- `anthropic_version` is injected into the request body automatically (Vertex requires it there rather than as a header)

### Gemini via Vertex AI

Routes Gemini model requests through Google Cloud Vertex AI using the native `generateContent`/`streamGenerateContent` endpoints with OAuth2 authentication.

The native endpoints require only the standard `Vertex AI User` role (`roles/aiplatform.user`), unlike the OpenAI-compatible endpoint which requires `aiplatform.endpoints.predict`.

#### Setup

```yaml
providers:
  - name: "vertex-gemini"
    type: "gemini"
    auth_mode: "vertex"
    project: "my-gcp-project"
    region: "us-central1"  # defaults to "us-central1"
    # credentials_file: "/path/to/service-account.json"  # optional; omit for ADC

model_list:
  - model_name: "gemini-flash"
    provider: "vertex-gemini"
    upstream_model: "google/gemini-2.5-flash"
  - model_name: "vg/*"         # wildcard: vg/<model> → vertex-gemini
    provider: "vertex-gemini"
```

#### Pricing

Built-in pricing for Gemini 3.1, 3, 2.5, and 2.0 models is applied automatically. Override with `metadata` on individual model entries.

#### Notes

- Uses native Gemini endpoints (`generateContent` / `streamGenerateContent?alt=sse`)
- Model names use the `google/` prefix (e.g. `google/gemini-2.5-flash`, `google/gemini-3-pro-preview`)
- The `google/` prefix is stripped from the model name when constructing the URL path
- Translation between client API format and native Gemini format is handled automatically

### Gemini Provider

Routes Gemini model requests through the Gemini Developer API using `X-Goog-Api-Key` authentication.

#### Setup

```yaml
providers:
  - name: "gemini"
    type: "gemini"
    auth_mode: "proxy_key"
    api_key: "${GEMINI_API_KEY}"
    # base_url: "https://generativelanguage.googleapis.com"  # optional default

model_list:
  - model_name: "gemini-flash"
    provider: "gemini"
    upstream_model: "gemini-2.5-flash"
  - model_name: "gem/*"        # wildcard: gem/<model> → gemini
    provider: "gemini"
```

#### Notes

- Uses native Gemini endpoints (`generateContent` / `streamGenerateContent?alt=sse`)
- Supports `auth_mode: proxy_key` with a configured `api_key`
- Supports `auth_mode: forward` by forwarding the incoming `X-Goog-Api-Key` header upstream
- Accepts upstream model names with or without the `google/` prefix; the provider strips it before building the URL
- Built-in Gemini pricing defaults are applied automatically for known Gemini models

---

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
| `request_log_body_max_bytes` | `4194304` | Maximum bytes retained for stored request/response bodies after redaction |

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

---

## Full Config Reference

```yaml
# REQUIRED. Web UI password. Set here or via GLITCHGATE_MASTER_KEY.
master_key: "change-me-to-something-secure"

# Address to listen on. Default: ":4000"
listen: ":4000"

# Path to the SQLite database. Default: "glitchgate.db". Supports ~.
# Note: Use either database_path (SQLite) OR database_url (PostgreSQL), not both.
database_path: "~/data/glitchgate/proxy.db"

# PostgreSQL connection string. Use instead of database_path for multi-instance setups.
# database_url: "postgres://user:pass@localhost/glitchgate?sslmode=require"

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
request_log_body_max_bytes: 4194304

providers:
  - name: "anthropic"
    type: "anthropic"            # default type; can be omitted
    base_url: "https://api.anthropic.com/v1"
    auth_mode: "proxy_key"
    api_key: "${ANTHROPIC_API_KEY}"
    default_version: "2023-06-01"

model_list:
  - model_name: "claude-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-6"
```

## Example Configs

### Minimal

```yaml
master_key: "change-me"

providers:
  - name: "anthropic"
    base_url: "https://api.anthropic.com/v1"
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
    base_url: "https://api.anthropic.com/v1"
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
    base_url: "https://api.anthropic.com/v1"
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

### Anthropic + Vertex AI Claude

```yaml
master_key: "change-me"

providers:
  - name: "anthropic"
    base_url: "https://api.anthropic.com/v1"
    auth_mode: "proxy_key"
    api_key: "${ANTHROPIC_API_KEY}"
    default_version: "2023-06-01"
  - name: "vertex-claude"
    type: "anthropic"
    auth_mode: "vertex"
    project: "my-gcp-project"
    region: "us-east5"

model_list:
  - model_name: "claude-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-6"
  - model_name: "vertex/*"
    provider: "vertex-claude"
  - model_name: "resilient-sonnet"
    fallbacks: ["claude-sonnet", "vertex-sonnet"]
  - model_name: "vertex-sonnet"
    provider: "vertex-claude"
    upstream_model: "claude-sonnet-4-6-20250514"
```

### Environment-Only (no config file)

```bash
export GLITCHGATE_MASTER_KEY="change-me"
export GLITCHGATE_LISTEN=":8080"
export GLITCHGATE_DATABASE_PATH="/var/lib/glitchgate/data.db"
```

`providers`, `model_list`, and `oidc` require a config file.
