# Contract: Admin / Web UI API

The proxy serves a web UI and its backing API on the same port as
the proxy endpoints. All admin API routes are prefixed with `/ui/api/`
and require session authentication (except the login endpoint).

## Authentication

### Login

```
POST /ui/api/login
Content-Type: application/json

{"master_key": "your-master-key"}
```

**Success (200)**:
```json
{
  "session_token": "sess_abc123...",
  "expires_at": "2026-03-12T10:00:00Z"
}
```

**Failure (401)**:
```json
{"error": "Invalid master key"}
```

### Session Usage

All subsequent requests include the session token:
```
Cookie: session=sess_abc123...
```
Or:
```
Authorization: Bearer sess_abc123...
```

### Logout

```
POST /ui/api/logout
```

Invalidates the current session.

## Logs API

### List Logs

```
GET /ui/api/logs?page=1&per_page=50&model=claude-sonnet&status=200&key=llmp_sk_&from=2026-03-01&to=2026-03-11&sort=timestamp&order=desc
```

All query parameters are optional.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| page | int | 1 | Page number |
| per_page | int | 50 | Items per page (max 100) |
| model | string | - | Filter by model_requested |
| status | int | - | Filter by HTTP status code |
| key | string | - | Filter by proxy key prefix |
| from | date | - | Start date (inclusive) |
| to | date | - | End date (inclusive) |
| sort | string | timestamp | Sort field: timestamp, latency_ms, input_tokens, output_tokens, estimated_cost_usd |
| order | string | desc | Sort order: asc, desc |

**Response (200)**:
```json
{
  "logs": [
    {
      "id": "uuid",
      "timestamp": "2026-03-11T14:30:00Z",
      "source_format": "anthropic",
      "provider_name": "anthropic",
      "model_requested": "claude-sonnet",
      "model_upstream": "claude-sonnet-4-20250514",
      "proxy_key_prefix": "llmp_sk_",
      "proxy_key_label": "claude-code",
      "input_tokens": 250,
      "output_tokens": 1200,
      "latency_ms": 3400,
      "status": 200,
      "estimated_cost_usd": 0.0195,
      "is_streaming": true,
      "error_details": null
    }
  ],
  "total": 1234,
  "page": 1,
  "per_page": 50
}
```

### Get Log Detail

```
GET /ui/api/logs/:id
```

**Response (200)**:
```json
{
  "id": "uuid",
  "timestamp": "2026-03-11T14:30:00Z",
  "source_format": "anthropic",
  "provider_name": "anthropic",
  "model_requested": "claude-sonnet",
  "model_upstream": "claude-sonnet-4-20250514",
  "proxy_key_prefix": "llmp_sk_",
  "proxy_key_label": "claude-code",
  "input_tokens": 250,
  "output_tokens": 1200,
  "latency_ms": 3400,
  "status": 200,
  "estimated_cost_usd": 0.0195,
  "is_streaming": true,
  "error_details": null,
  "request_body": "{...redacted...}",
  "response_body": "{...}"
}
```

## Costs API

### Get Cost Summary

```
GET /ui/api/costs?from=2026-03-01&to=2026-03-11&group_by=model&key=llmp_sk_
```

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| from | date | 30 days ago | Start date |
| to | date | today | End date |
| group_by | string | model | Grouping: model, key, day, week, month |
| key | string | - | Filter by proxy key prefix |

**Response (200)**:
```json
{
  "total_cost_usd": 42.50,
  "total_input_tokens": 1250000,
  "total_output_tokens": 3400000,
  "total_requests": 892,
  "breakdown": [
    {
      "group": "claude-sonnet-4-20250514",
      "cost_usd": 30.25,
      "input_tokens": 900000,
      "output_tokens": 2500000,
      "requests": 650
    },
    {
      "group": "claude-opus-4-20250514",
      "cost_usd": 12.25,
      "input_tokens": 350000,
      "output_tokens": 900000,
      "requests": 242
    }
  ],
  "from": "2026-03-01",
  "to": "2026-03-11"
}
```

### Get Cost Over Time

```
GET /ui/api/costs/timeseries?from=2026-03-01&to=2026-03-11&interval=day&key=llmp_sk_
```

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| interval | string | day | Bucket size: day, week, month |
| from | date | 30 days ago | Start date |
| to | date | today | End date |
| key | string | - | Filter by proxy key prefix |

**Response (200)**:
```json
{
  "interval": "day",
  "data": [
    {"date": "2026-03-01", "cost_usd": 3.20, "requests": 85},
    {"date": "2026-03-02", "cost_usd": 5.10, "requests": 120},
    {"date": "2026-03-03", "cost_usd": 2.80, "requests": 72}
  ],
  "from": "2026-03-01",
  "to": "2026-03-11"
}
```

## Web UI Pages

The web UI is served as HTML pages at `/ui/`:

| Route | Description |
|-------|-------------|
| `GET /ui/login` | Login page (master key input) |
| `GET /ui/` | Dashboard / redirect to logs |
| `GET /ui/logs` | Log viewer (table with filters, click for detail) |
| `GET /ui/logs/:id` | Log detail view (full request/response) |
| `GET /ui/costs` | Cost dashboard (totals, charts, breakdowns) |

Pages use Go `html/template` with HTMX for dynamic interactions
(filtering, pagination, sorting) without full page reloads.
Static assets (CSS, JS) are embedded via `go:embed`.
