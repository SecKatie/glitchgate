# Configuration Reference

glitchgate is configured via a YAML file, environment variables, or a combination of both.

## Config File Location

The config file is searched in the following order:

1. Path passed via `--config` flag (e.g. `glitchgate --config /path/to/config.yaml serve`)
2. `~/.config/glitchgate/config.yaml`
3. `./config.yaml` (current working directory)
4. `/etc/glitchgate/config.yaml`

The first file found wins. If no file is found, glitchgate falls back to defaults and environment variables.

## Environment Variables

Simple scalar keys can be set via environment variables with the prefix `GLITCHGATE_`. Dots and nested keys use underscores:

| Config Key      | Environment Variable          |
|-----------------|-------------------------------|
| `master_key`    | `GLITCHGATE_MASTER_KEY`       |
| `listen`        | `GLITCHGATE_LISTEN`           |
| `database_path` | `GLITCHGATE_DATABASE_PATH`    |

Environment variables override config file values.

Note: `providers`, `model_list`, and `pricing` cannot be set via environment variables — they require a config file.

## Full Config Reference

```yaml
# REQUIRED. Password for the web UI. Set here or via GLITCHGATE_MASTER_KEY.
master_key: "change-me-to-something-secure"

# Address to listen on. Default: ":4000"
listen: ":4000"

# Path to the SQLite database file. Default: "glitchgate.db"
# Supports ~ for home directory. Parent directories are created automatically.
database_path: "~/data/glitchgate/proxy.db"

# IANA timezone name for the web UI. Default: "UTC"
# Affects how timestamps are displayed in logs/costs and how date ranges default.
# Example: "America/New_York", "America/Chicago", "America/Los_Angeles"
timezone: "America/New_York"

# Upstream LLM providers.
providers:
  - name: "anthropic"               # Unique name, referenced by model_list
    type: "anthropic"               # Provider type. Default: "anthropic"
    base_url: "https://api.anthropic.com"
    auth_mode: "proxy_key"           # "proxy_key" or "forward" (see below)
    api_key: "${ANTHROPIC_API_KEY}"  # Supports $ENV_VAR expansion
    default_version: "2023-06-01"    # Anthropic-Version header

  - name: "copilot"                  # GitHub Copilot provider
    type: "github_copilot"
    # token_dir: "~/.config/glitchgate/copilot/"  # Optional; default shown

# Maps client-facing model names to upstream provider + model.
# Use 'fallbacks' to define virtual models with automatic failover.
model_list:
  - model_name: "claude-sonnet"              # Name clients send in requests
    provider: "anthropic"                    # Must match a provider name above
    upstream_model: "claude-sonnet-4-20250514" # Actual model sent upstream
  - model_name: "claude-resilient"           # Virtual model with fallback chain
    fallbacks: ["claude-sonnet"]             # Try these in order on 5xx/429
  - model_name: "gc/*"                       # Wildcard: gc/<model> → copilot
    provider: "copilot"

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
| `type`            | No       | Provider type: `"anthropic"` (default), `"github_copilot"`. Determines how requests are formatted and sent upstream |
| `base_url`        | Depends  | Upstream API base URL. Required for `anthropic` type. Not used by `github_copilot` (auto-discovered) |
| `auth_mode`       | Depends  | How the proxy authenticates with the upstream (see below). Not used by `github_copilot` (manages its own auth) |
| `api_key`         | Depends  | API key for `proxy_key` mode. Supports `${ENV_VAR}` expansion |
| `default_version` | No       | Sets the `anthropic-version` header if the client doesn't send one |
| `token_dir`       | No       | Token storage directory for `github_copilot`. Default: `~/.config/glitchgate/copilot/` |

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

### GitHub Copilot Provider

The `github_copilot` provider type proxies requests through the GitHub Copilot API. It manages its own authentication via OAuth device flow and automatically injects the required editor-simulation headers.

#### Setup

**Step 1: Authenticate with GitHub**

Run the device flow once to obtain and store OAuth tokens:

```sh
glitchgate auth copilot
```

This opens a browser-based GitHub authorization flow. Once approved, tokens are saved to `~/.config/glitchgate/copilot/` (or the directory specified by `--token-dir`). The proxy refreshes the short-lived Copilot session token automatically.

You can run `glitchgate auth copilot` at any time — before configuring the provider, while the proxy is running, or on a different machine. It is fully independent of the proxy server.

**Step 2: Configure the provider**

```yaml
providers:
  - name: "copilot"
    type: "github_copilot"
    # token_dir: "~/.config/glitchgate/copilot/"  # default; override if needed
```

No `base_url`, `auth_mode`, or `api_key` are needed — the Copilot provider discovers the API endpoint from the session token and handles authentication internally.

**Step 3: Add model mappings**

Use wildcard routing to expose all Copilot models under a prefix:

```yaml
model_list:
  - model_name: "gc/*"
    provider: "copilot"
```

Clients can then request any model as `gc/<model-name>` — for example, `gc/claude-sonnet-4.6` or `gc/gpt-5.2`. The proxy strips the `gc/` prefix and sends the remainder as the upstream model name.

You can also create exact mappings for specific models:

```yaml
model_list:
  - model_name: "copilot-claude"
    provider: "copilot"
    upstream_model: "claude-sonnet-4.6"
```

#### Format-Aware Routing

The Copilot API speaks OpenAI Chat Completions format. The proxy handles format translation automatically:

- **OpenAI-format clients** (`/v1/chat/completions`) — requests are forwarded directly to Copilot with no translation overhead.
- **Anthropic-format clients** (`/v1/messages`) — requests are translated from Anthropic format to OpenAI format before sending, and responses are translated back.

This means existing Anthropic-format tools (like Claude Code) can use Copilot models transparently.

#### Pricing

Copilot models are subscription-based with no per-token cost. Built-in pricing entries for common Copilot models are set to $0. To track notional costs, override with custom pricing:

```yaml
pricing:
  - model: "claude-sonnet-4.6"  # upstream model name (after prefix stripping)
    input_per_million: 3.00
    output_per_million: 15.00
```

#### Token Storage

Tokens are stored as JSON files with restrictive permissions:

| File | Contents | Permissions |
|------|----------|-------------|
| `github_token.json` | Long-lived GitHub OAuth token | `0600` |
| `copilot_token.json` | Short-lived Copilot session token (cache) | `0600` |

The directory is created with `0700` permissions. The session token is refreshed automatically when it expires (with a 60-second buffer).

#### Multiple GitHub Copilot Accounts

You can configure more than one `github_copilot` provider — for example, to separate work and personal accounts, or to route different model prefixes to different Copilot subscriptions. Each provider must have a unique `token_dir` (required when more than one copilot provider is present).

**Config:**

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

**Auth (once per account):**

```bash
glitchgate auth copilot --name copilot-work
glitchgate auth copilot --name copilot-personal
```

`--name` looks up the provider's `token_dir` from your config file automatically. You can also use `--token-dir` directly if you prefer to specify the path explicitly (mutually exclusive with `--name`).

To replace an existing account's credentials, add `--force`:

```bash
glitchgate auth copilot --name copilot-work --force
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

### Virtual Models and Fallback Chains

A model entry with a `fallbacks` list is a **virtual model**. Instead of routing to a single provider, it defines an ordered list of concrete model names to try. The proxy attempts each entry in order, moving to the next when a provider returns a `5xx` error or `429 Too Many Requests`.

```yaml
model_list:
  # Virtual model — tries primary first, then falls back to secondary
  - model_name: "claude-resilient"
    fallbacks: ["primary-sonnet", "secondary-sonnet"]

  # Concrete entries referenced by the virtual model
  - model_name: "primary-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-20250514"
  - model_name: "secondary-sonnet"
    provider: "anthropic-backup"
    upstream_model: "claude-sonnet-4-20250514"
```

**Fallback rules:**

| Upstream response | Behavior |
|-------------------|----------|
| `5xx` (server error) | Try next entry in the chain |
| `429 Too Many Requests` | Try next entry in the chain |
| `4xx` (other client error) | Return the error immediately — no retry |
| Network error | Try next entry if one remains; otherwise `502 Bad Gateway` |
| All entries exhausted | Return `503 Service Unavailable` |

**Fields:**

| Field       | Required | Description |
|-------------|----------|-------------|
| `model_name`| Yes      | Client-facing name |
| `fallbacks` | Yes (for virtual) | Ordered list of concrete model names to try |
| `provider`  | Yes (for concrete) | Provider name — must match a `providers` entry |
| `upstream_model` | Yes (for concrete) | Model name sent to the upstream provider |

`fallbacks` and `provider`/`upstream_model` are mutually exclusive — a model entry is either virtual (has `fallbacks`) or concrete (has `provider` + `upstream_model`), never both.

**Nested virtual models** are supported and flattened at startup:

```yaml
model_list:
  - model_name: "best-available"
    fallbacks: ["tier-1", "tier-2"]
  - model_name: "tier-1"
    fallbacks: ["provider-a-sonnet", "provider-b-sonnet"]
  - model_name: "tier-2"
    provider: "anthropic-fallback"
    upstream_model: "claude-haiku-4-20250514"
  - model_name: "provider-a-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-20250514"
  - model_name: "provider-b-sonnet"
    provider: "anthropic-backup"
    upstream_model: "claude-sonnet-4-20250514"
```

A request to `best-available` tries: `provider-a-sonnet` → `provider-b-sonnet` → `tier-2` in that order.

**Cycle detection:** glitchgate validates fallback chains at startup. Circular references (e.g. A → B → A) are rejected with a descriptive error message.

**Logging:** Each request log records `fallback_attempts` — the number of chain entries that were attempted. A direct hit records `1`; if the second entry succeeds it records `2`; and so on.

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
export GLITCHGATE_MASTER_KEY="change-me"
export GLITCHGATE_LISTEN=":8080"
export GLITCHGATE_DATABASE_PATH="/var/lib/glitchgate/data.db"
```

Note: `providers`, `model_list`, and `pricing` require a config file.

### Multiple providers with wildcard routing

```yaml
master_key: "change-me"
listen: ":4000"
database_path: "/var/lib/glitchgate/proxy.db"

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
  # Direct Anthropic access
  - model_name: "claude-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-20250514"

  # All Copilot models under gc/ prefix
  - model_name: "gc/*"
    provider: "copilot"
```

With this config, `claude-sonnet` goes directly to Anthropic, while `gc/claude-sonnet-4.6`, `gc/gpt-5.2`, etc. route through Copilot.

---

## OIDC Authentication

glitchgate supports OpenID Connect (authorization code flow + PKCE) for web UI authentication. When configured, users sign in via your identity provider (Okta, Google Workspace, Azure AD, etc.) instead of a static password.

### Configuration

```yaml
oidc:
  issuer_url: "https://accounts.example.com"    # OIDC provider discovery URL
  client_id: "your-client-id"
  client_secret: "${OIDC_CLIENT_SECRET}"         # Supports $ENV_VAR expansion
  redirect_url: "https://proxy.example.com/ui/auth/callback"
  scopes: ["openid", "email", "profile"]         # Default; rarely needs changing
```

| Field          | Required | Description |
|----------------|----------|-------------|
| `issuer_url`   | Yes      | OIDC issuer base URL (must expose `/.well-known/openid-configuration`) |
| `client_id`    | Yes      | OAuth client ID from your IDP |
| `client_secret`| Yes      | OAuth client secret. Use `${ENV_VAR}` to avoid storing secrets in config files |
| `redirect_url` | Yes      | Callback URL registered with your IDP. Must be `https://` in production |
| `scopes`       | No       | Requested OIDC scopes. Default: `["openid", "email", "profile"]` |

### IDP Setup

Register a web application / OAuth client with your IDP and set the **redirect URI** to:
```
https://your-proxy-host/ui/auth/callback
```

Ensure the application returns at minimum the `email` and (optionally) `name`/`preferred_username` claims.

### Role Model

| Role           | Capabilities |
|----------------|-------------|
| `global_admin` | Full access: manage users, teams, all keys, all logs and costs |
| `team_admin`   | Manage own team members; see team-scoped logs, costs, and keys |
| `member`       | See own keys, logs, and costs only |

The **first user** to authenticate via OIDC is automatically granted `global_admin`. Subsequent users are created as `member` and can be promoted in the **Users** management page.

### Break-Glass Access

If OIDC is unavailable, the master key login form can be accessed at:

```
/ui/login?master=1
```

This form is hidden by default when OIDC is configured. Use `master_key` in your config to set the break-glass password.

> **Important**: The break-glass master key session bypasses OIDC entirely. Treat it like a root password — store it in a secrets manager and restrict access.
