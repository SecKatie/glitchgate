# glitchgate Backlog

> User stories for feature development. Prioritized by value and effort.
> Last updated: 2026-03-17 after comprehensive codebase review (70+ files, 12K+ lines Go)
>
> Key observations from code review:
> - Metrics implementation: **Complete** (`internal/metrics/metrics.go`)
> - Security hardening Phase 1: **Complete** (CSRF, CSP, constant-time comparison, session hashing)
> - Critical gaps: No circuit breaker, flat-rate pricing only, no key-level scoping

---

## Theme: Security & Access Control

### US-001: Per-Key Model Allowlist
**Priority:** P0 | **Estimate:** M (3-5 days) | **Status:** Schema changes required

As an admin, I want to restrict which models an API key can access so that I can enforce least-privilege access and prevent misuse of expensive models.

**Current State:**
- `proxy_keys` table has no scoping columns (`queries/proxy_keys.sql:1-19`)
- Rate limiter uses global config only (`internal/ratelimit/limiter.go:28-46`)
- All keys have identical access to all models

**Acceptance Criteria:**
- [ ] Admin can specify allowlist of model patterns (exact match + wildcard) per key
- [ ] Requests with non-allowed models return 403 with clear error message
- [ ] Allowlist is editable via CLI (`keys update --allow-models`) and UI
- [ ] Migration adds `allowed_models` column to proxy_keys table

**Technical Notes:**
- Add `allowed_models` text column to `proxy_keys` table (JSON array)
- Modify `ProxyKeyAuthStore` interface to include allowed models
- Add validation in middleware before model resolution

---

### US-002: Per-Key Custom Rate Limits
**Priority:** P0 | **Estimate:** S (2 days) | **Status:** Requires rate limiter refactor

As an admin, I want to set custom rate limits per API key so that I can give different quotas to different teams or users.

**Current State:**
- `Limiter` struct has global `tokensPerSec` and `burst` (`internal/ratelimit/limiter.go:16-25`)
- No per-key override mechanism — all keys share same rate config
- `Allow(key string)` signature supports keys but uses global limits

**Acceptance Criteria:**
- [ ] Admin can set per-key `rate_limit_per_minute` and `rate_limit_burst`
- [ ] Defaults to global config if not specified per-key
- [ ] Editable via CLI and UI

---

### US-003: Per-Key Metadata Tags
**Priority:** P1 | **Estimate:** S (1 day)

As an admin, I want to tag API keys with metadata (e.g., "engineering", "marketing", "project-x") so that I can identify key owners and purpose.

**Acceptance Criteria:**
- [ ] Add optional `tags` column (JSON) to proxy_keys
- [ ] Display tags in key list UI
- [ ] Filter keys by tag in CLI

---

## Theme: Operations & Reliability

### US-004: Provider Health Dashboard
**Priority:** P1 | **Estimate:** S (2-3 days)

As an ops engineer, I want to see provider health status in the UI so that I can quickly identify issues.

**Acceptance Criteria:**
- [ ] Dashboard shows per-provider: success rate, avg latency, error count (last 24h)
- [ ] Color-coded status (green/yellow/red)
- [ ] Metric sources: request_logs table aggregation

**Technical Notes:**
- Add store query for provider health aggregation
- New UI widget in dashboard template

---

### US-005: Circuit Breaker for Providers
**Priority:** P1 | **Estimate:** M (3 days) | **Status:** Critical gap — currently no provider health tracking

As an ops engineer, I want the proxy to automatically pause failing providers so that downstream services aren't affected by upstream outages.

**Current State:**
- `executeFallbackChain()` in `pipeline.go:87-108` retries immediately on 5xx/429
- No health state tracking — every request hits all providers regardless of recent failures
- Thundering herd risk on recovering providers

**Acceptance Criteria:**
- [ ] Track consecutive errors (5xx, 429) per provider
- [ ] After N consecutive errors, enter cooldown period (configurable, default 30s)
- [ ] During cooldown, skip provider in fallback chain
- [ ] Log circuit breaker state changes
- [ ] Metrics for circuit breaker state

**Technical Notes:**
- Add in-memory health tracker (per-instance state is acceptable)
- Store consecutive errors per provider, timestamp of first error
- Modify `executeFallbackChain()` to skip providers with open circuits
- Config: `circuit_breaker_threshold`, `circuit_breaker_cooldown`
- Add metric: `glitchgate_provider_circuit_breaker_state`

---

### US-006: Structured Logging & OTLP Export
**Priority:** P2 | **Estimate:** M (3 days)

As an SRE, I want logs to be exportable to external systems so that I can aggregate with other services.

**Acceptance Criteria:**
- [ ] Support stdout, file, syslog, and OTLP (HTTP/gRPC) exporters
- [ ] Config: `log_output`, `otlp_endpoint`
- [ ] Add correlation ID (X-Request-ID) to all log entries
- [ ] Runtime log level adjustment via signal or endpoint

---

### US-007: Built-in TLS Termination
**Priority:** P2 | **Estimate:** M (3-5 days)

As an operator, I want glitchgate to handle TLS so that I don't need a reverse proxy for simple deployments.

**Acceptance Criteria:**
- [ ] Config: `tls_cert_file`, `tls_key_file`
- [ ] Optional ACME (Let's Encrypt) with `tls_acme: true`, `tls_acme_email`
- [ ] Graceful upgrade (no dropped connections)

---

## Theme: Cost Management

### US-008: Tiered Pricing Support
**Priority:** P1 | **Estimate:** M (3 days) | **Status:** Calculator only supports flat rates

As a finance admin, I want pricing to account for tiered rates (e.g., higher rates after 100K tokens) so that cost reports are accurate.

**Current State:**
- `pricing.Entry` struct has flat rates only (`internal/pricing/calculator.go:6-12`)
- `Calculate()` applies single rate regardless of token count (`internal/pricing/calculator.go:37-56`)
- OpenAI GPT-4o has tiered pricing beyond 128K context — currently undercounted

**Acceptance Criteria:**
- [ ] Pricing Entry supports threshold-based tiers
- [ ] Config example: `tiers: [{threshold: 100000, rate: 15.00}, {threshold: null, rate: 10.00}]`
- [ ] Default pricing tables include tiers where applicable (e.g., o1-preview)

**Technical Notes:**
- Extend `pricing.Entry` struct with `Tiers []Tier`
- Modify `Calculator.Calculate` to apply tier logic

---

### US-009: Team-Scoped Cost Visibility
**Priority:** P1 | **Estimate:** S (2 days)

As a team admin, I want to see my team's costs so that I can monitor our budget without asking a global admin.

**Acceptance Criteria:**
- [ ] Team admin sees only their team's costs on `/ui/costs`
- [ ] Dashboard shows team budget status widget
- [ ] Filter costs by team in queries

---

### US-010: Cost Export (CSV/JSON)
**Priority:** P1 | **Estimate:** S (2 days)

As a finance user, I want to export cost data so that I can do accounting in external tools.

**Acceptance Criteria:**
- [ ] Export button on cost page: CSV and JSON formats
- [ ] Filter by date range, team, user, model
- [ ] Include all cost columns

---

### US-011: Audit Log Enhancements
**Priority:** P2 | **Estimate:** S (2-3 days)

As a compliance officer, I want searchable audit logs so that I can investigate incidents.

**Acceptance Criteria:**
- [ ] Filter audit log by action type, actor, date range
- [ ] CSV export
- [ ] Configurable retention (default: 90 days)
- [ ] Webhook option for sensitive actions

---

## Theme: Developer Experience

### US-012: Onboarding Documentation UI
**Priority:** P1 | **Estimate:** S (2 days)

As a new user, I want in-app documentation so that I can get started quickly.

**Acceptance Criteria:**
- [ ] `/ui/docs` page with endpoint reference
- [ ] Code examples: curl, Python, JavaScript
- [ ] New deployment checklist banner on dashboard
- [ ] Inline help tooltips on complex config fields

---

### US-013: Request Replay & Debug Tools
**Priority:** P2 | **Estimate:** S (2-3 days)

As a developer, I want to replay failed requests so that I can debug issues.

**Acceptance Criteria:**
- [ ] "Copy as cURL" button on log detail
- [ ] "Copy request JSON" / "Copy response JSON"
- [ ] "Replay" button (POST to self with same body)

---

## Theme: Self-Service & User Empowerment

### US-014: Member Self-Service Portal
**Priority:** P2 | **Estimate:** M (3-5 days)

As a team member, I want to see my own usage and manage my own keys so that I don't need admin help.

**Acceptance Criteria:**
- [ ] "My Usage" page: personal cost breakdown, token counts
- [ ] "My Keys" page: create/revoke own keys (within budget)
- [ ] Personal budget status widget

---

### US-015: Request/Response Caching
**Priority:** P2 | **Estimate:** M (3-5 days)

As an admin, I want caching for identical requests so that I can reduce costs for repeated queries.

**Acceptance Criteria:**
- [ ] Exact-match cache (hash of model + messages)
- [ ] Config: enable/disable, TTL, max size
- [ ] Cache bypass: `X-Cache-Control: no-cache` header
- [ ] Cache hit/miss metrics

---

## Theme: Advanced Features

### US-016: Model Routing Policies
**Priority:** P3 | **Estimate:** L (1-2 weeks)

As an ops engineer, I want dynamic routing based on request characteristics so that I can optimize cost and performance.

**Acceptance Criteria:**
- [ ] Token-count-based routing (>N tokens → model X)
- [ ] Time-of-day routing (off-peak → expensive model)
- [ ] A/B split routing (percentage to model A, rest to model B)

---

### US-017: OpenAPI Specification
**Priority:** P3 | **Estimate:** M (3 days)

As an API consumer, I want an OpenAPI spec so that I can generate SDKs and explore the API.

**Acceptance Criteria:**
- [ ] Serve OpenAPI 3.1 at `/openapi.json`
- [ ] Interactive docs at `/docs` (Swagger UI or Scalar)

---

### US-018: Multi-Instance / HA Support
**Priority:** P3 | **Estimate:** L (1-2 weeks)

As an enterprise user, I want to run multiple instances so that I have high availability.

**Acceptance Criteria:**
- [ ] Validate PostgreSQL HA with pgbouncer
- [ ] External session store (PostgreSQL)
- [ ] Load balancer compatibility documentation

---

## Backlog Summary

| ID | User Story | Priority | Estimate |
|----|------------|----------|----------|
| US-001 | Per-Key Model Allowlist | P0 | M |
| US-002 | Per-Key Custom Rate Limits | P0 | S |
| US-003 | Per-Key Metadata Tags | P1 | S |
| US-004 | Provider Health Dashboard | P1 | S |
| US-005 | Circuit Breaker for Providers | P1 | M |
| US-006 | Structured Logging & OTLP | P2 | M |
| US-007 | Built-in TLS Termination | P2 | M |
| US-008 | Tiered Pricing Support | P1 | M |
| US-009 | Team-Scoped Cost Visibility | P1 | S |
| US-010 | Cost Export (CSV/JSON) | P1 | S |
| US-011 | Audit Log Enhancements | P2 | S |
| US-012 | Onboarding Docs UI | P1 | S |
| US-013 | Request Replay & Debug | P2 | S |
| US-014 | Member Self-Service Portal | P2 | M |
| US-015 | Request/Response Caching | P2 | M |
| US-016 | Model Routing Policies | P3 | L |
| US-017 | OpenAPI Specification | P3 | M |
| US-018 | Multi-Instance / HA | P3 | L |

**Estimate Key:** XS = hours, S = 1-2 days, M = 3-5 days, L = 1-2 weeks

---

## Additional Stories from README

### US-019: Per-Key UI Login
**Priority:** P1 | **Estimate:** S (2 days) | **Status:** Not implemented

As an API key holder without SSO credentials, I want to log into the web UI using my proxy key so that I can view my own logs and costs.

**Current State:**
- Session types: `master_key`, `oidc` (`internal/auth/session.go:43-67`)
- No `proxy_key` session type
- `listKeysForSession()` in `handlers.go` has basic role filtering

**Acceptance Criteria:**
- [ ] Login page accepts proxy key (read-only access)
- [ ] Key-based session sees only that key's logs and costs
- [ ] Clear visual distinction between SSO and key-based sessions
- [ ] Audit log records key-based logins

**Technical Notes:**
- Add `proxy_key` session type
- Modify `AuthMiddleware` to support key-based session creation
- Scope all queries to the specific key

---