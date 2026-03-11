# Data Model: Core Proxy with Logging & Cost Monitoring

**Feature**: 001-core-proxy-logging
**Date**: 2026-03-11

## Entities

### 1. proxy_keys

Stores proxy API keys used to authenticate incoming requests.

| Field | Type | Constraints | Description |
|-------|------|-------------|-------------|
| id | TEXT | PK | UUID v4 |
| key_hash | TEXT | UNIQUE, NOT NULL | bcrypt hash of the full API key |
| key_prefix | TEXT | NOT NULL | First 8 characters of the key (for display/identification) |
| label | TEXT | NOT NULL | Human-readable label (e.g., "claude-code", "dev-scripts") |
| created_at | DATETIME | NOT NULL | ISO 8601 timestamp |
| revoked_at | DATETIME | NULL | Set when key is revoked; NULL = active |

**Notes**:
- The full API key is only shown once at creation time; only the
  hash is stored.
- `key_prefix` allows users to identify which key was used without
  exposing the full key.
- Revoked keys remain in the table for audit trail; queries filter
  on `revoked_at IS NULL` for active keys.

### 2. request_logs

Stores every proxied request/response pair with metadata.

| Field | Type | Constraints | Description |
|-------|------|-------------|-------------|
| id | TEXT | PK | UUID v4 |
| proxy_key_id | TEXT | FK → proxy_keys.id, NOT NULL | Which API key made the request |
| timestamp | DATETIME | NOT NULL, INDEX | When the request was received |
| source_format | TEXT | NOT NULL | "anthropic" or "openai" |
| provider_name | TEXT | NOT NULL | Provider config entry used (e.g., "claude-max", "anthropic") |
| model_requested | TEXT | NOT NULL | Client-facing model name requested |
| model_upstream | TEXT | NOT NULL | Actual model name sent upstream |
| input_tokens | INTEGER | NOT NULL, DEFAULT 0 | Input/prompt token count from upstream response |
| output_tokens | INTEGER | NOT NULL, DEFAULT 0 | Output/completion token count from upstream response |
| latency_ms | INTEGER | NOT NULL | Total round-trip time in milliseconds |
| status | INTEGER | NOT NULL | HTTP status code returned to client |
| request_body | TEXT | NOT NULL | Request JSON with API keys redacted |
| response_body | TEXT | NOT NULL | Response JSON (full for non-streaming, reconstructed for streaming) |
| estimated_cost_usd | REAL | NULL | Calculated cost in USD; NULL if pricing unknown for model |
| error_details | TEXT | NULL | Error message if request failed |
| is_streaming | INTEGER | NOT NULL, DEFAULT 0 | 1 if streaming request, 0 otherwise |

**Indexes**:
- `idx_request_logs_timestamp` on `timestamp` (for chronological listing)
- `idx_request_logs_proxy_key_id` on `proxy_key_id` (for per-key filtering)
- `idx_request_logs_model_requested` on `model_requested` (for model filtering)
- `idx_request_logs_status` on `status` (for status filtering)

**Notes**:
- `request_body` has API keys and Authorization headers redacted
  before storage.
- For streaming responses, `response_body` contains the
  reconstructed full response (assembled from SSE chunks).
- `input_tokens` and `output_tokens` are extracted from the upstream
  provider's response (usage field).
- `estimated_cost_usd` is NULL when the model has no configured
  pricing, rather than 0.

## Configuration-Only Entities (not in database)

### 3. Provider (config)

Defined in the configuration file, not stored in the database.

```yaml
providers:
  - name: "anthropic"
    base_url: "https://api.anthropic.com"
    auth_mode: "proxy_key"      # proxy sends its own API key
    api_key: "${ANTHROPIC_API_KEY}"
    default_version: "2023-06-01"

  - name: "claude-max"
    base_url: "https://api.anthropic.com"
    auth_mode: "forward"        # forwards client's Authorization header
    default_version: "2023-06-01"
```

| Field | Type | Description |
|-------|------|-------------|
| name | string | Unique identifier for this provider entry |
| base_url | string | Upstream API base URL |
| auth_mode | string | "proxy_key" or "forward" |
| api_key | string | Upstream API key (required if auth_mode=proxy_key, omitted for forward) |
| default_version | string | Default API version header (e.g., anthropic-version) |

### 4. Model Mapping (config)

Defined in the configuration file.

```yaml
model_list:
  - model_name: "claude-sonnet"
    provider: "anthropic"
    upstream_model: "claude-sonnet-4-20250514"

  - model_name: "claude-sonnet-max"
    provider: "claude-max"
    upstream_model: "claude-sonnet-4-20250514"

  - model_name: "claude-opus"
    provider: "anthropic"
    upstream_model: "claude-opus-4-20250514"
```

| Field | Type | Description |
|-------|------|-------------|
| model_name | string | Client-facing model name |
| provider | string | References a provider entry by name |
| upstream_model | string | Actual model identifier sent to the upstream API |

### 5. Model Pricing (config)

Defined in the configuration file. Ships with defaults for common
models; user overrides take precedence.

```yaml
pricing:
  - model: "claude-sonnet-4-20250514"
    input_per_million: 3.00
    output_per_million: 15.00

  - model: "claude-opus-4-20250514"
    input_per_million: 15.00
    output_per_million: 75.00
```

| Field | Type | Description |
|-------|------|-------------|
| model | string | Upstream model identifier (matches model_mapping.upstream_model) |
| input_per_million | float | Cost per million input tokens in USD |
| output_per_million | float | Cost per million output tokens in USD |

## Sessions (in-memory only)

Web UI sessions are stored in-memory with automatic expiry. They do
not need to survive restarts (user simply re-authenticates with the
master key).

| Field | Type | Description |
|-------|------|-------------|
| token | string | Cryptographically random session token |
| created_at | time | When the session was created |
| expires_at | time | When the session expires (configurable, default 24h) |

## Entity Relationships

```text
proxy_keys 1 ──── * request_logs
                     (proxy_key_id FK)

provider (config) 1 ──── * model_mapping (config)
                           (provider reference)

model_mapping (config) ──── request_logs
                           (model_requested matches model_name)

model_pricing (config) ──── request_logs
                           (estimated_cost_usd calculated from
                            model_upstream + token counts)
```

## Migration Strategy

- Use goose for SQL migrations, embedded in the binary via `go:embed`.
- Migration 001: Create `proxy_keys` table.
- Migration 002: Create `request_logs` table with indexes.
- Migrations run automatically on startup before the server begins
  accepting requests.
