# glitchgate Roadmap

> Generated from multi-perspective review: architecture, security, product/UX, operations.
> Reviewed by 4 specialist agents + PM synthesis.
> Last updated: 2026-03-17

## Current State Summary

glitchgate is a functional LLM API reverse proxy with:

- **3 API formats**: Anthropic Messages, OpenAI Chat Completions, OpenAI Responses
- **4 providers**: Anthropic, OpenAI-compatible, GitHub Copilot, Google Vertex AI
- **Web UI**: Dashboard, logs, costs, budgets, keys, users, teams, providers, models, audit
- **CLI**: serve, keys, users, teams, logs, costs, models, db, auth, version
- **Security**: Proxy key auth (bcrypt-hashed), OIDC SSO with PKCE, RBAC (global\_admin/team\_admin/member), rate limiting (IP + key), budget enforcement, security headers, audit logging, parameterized SQL via sqlc
- **Ops**: Graceful shutdown, health check, structured JSON logging, container-ready (docker-compose with security hardening), SQLite + PostgreSQL support

**Verdict**: Functionally complete for its stated use case. Security hardening complete (Phase 1 shipped). Ready for Phase 2 production readiness work.

---

## ~~P0 — Security Hardening~~ (COMPLETE)

All 7 security hardening items shipped. ~~BLOCKING production deployment~~ → Unblocked.

### ~~S1. Constant-Time Master Key Comparison~~ ✓
**Fix**: Replaced `!=` with `crypto/subtle.ConstantTimeCompare` in `internal/web/handlers.go`.

### ~~S2. CSRF Protection on State-Mutating Endpoints~~ ✓
**Fix**: Double-submit cookie pattern via `CSRFMiddleware`. HTMX auto-sends token. Applied to all protected `/ui` routes.

### ~~S3. Content-Security-Policy Header~~ ✓
**Fix**: Added CSP header in `SecurityHeadersMiddleware` with `frame-ancestors 'none'`, `base-uri 'self'`, `form-action 'self'`.

### ~~S4. Hash Session Tokens Before Storage~~ ✓
**Fix**: SHA-256 hash before DB storage/lookup/delete. Migration 027 clears existing sessions.

### ~~S5. Unit Tests for Security-Critical Packages~~ ✓
**Fix**: Added tests for `auth/keys`, `auth/session`, `ratelimit/limiter`, `oidc/state`, and CSRF middleware.

### ~~S6. Fix SSE Stream Connection Leaks~~ ✓
**Fix**: Added `ctx context.Context` to reverse stream functions with `closeOnCancel`. Call sites pass `r.Context()`.

### ~~S7. Size-Limit Upstream Response Bodies~~ ✓
**Fix**: All provider `io.ReadAll` calls wrapped with `io.LimitReader` (32 MB for responses, 1 MB for OAuth).

---

## P0 — Critical Features (Next)

### ~~1. Prometheus Metrics Endpoint~~ ✓ SHIPPED

**Perspective**: Operations
**Status**: Complete — metrics endpoint at `/metrics` with comprehensive instrumentation

Metrics available:
- Request count, latency histograms by model/provider/source_format
- Token counts (input, output, cache read, cache creation, reasoning)
- Cost accumulation in USD
- Streaming request counts
- Fallback attempts
- Active requests gauge
- Async logger stats (enqueued, persisted, dropped, failed)

Configurable via `metrics_enabled` in config.yaml (default: true)

### ~~2. Alerting & Notifications~~ ✗ DEPRECATED

**Status**: Out of scope — Alerting should be handled by external metrics systems (Grafana Alloy, Prometheus Alertmanager, etc.) rather than by this application.

### 3. API Key Scoping & Permissions
**Perspective**: Security, Product
**Effort**: M (3-5 days) | **Impact**: High
**Problem**: All proxy keys have identical access — any key can use any model/provider.
**Scope**:
- Per-key model allowlists (e.g., key X can only use `claude-sonnet-*`)
- Per-key custom rate limits (override global defaults)
- Per-key metadata tags for attribution
**Value**: Least-privilege enforcement, multi-tenant safety

---

## P1 — High Priority (Soon)

### 4. Provider Health Dashboard & Circuit Breaker
**Perspective**: Product/UX
**Effort**: S (2-3 days) | **Impact**: High
**Problem**: Users land on dashboard with no context. No endpoint docs, no getting-started guide, no curl examples. Developers waste time figuring out how to use the proxy.
**Scope**:
- `/ui/docs` page with endpoint reference, auth examples, curl/Python/JS snippets
- Dashboard welcome banner for new deployments (checklist: add provider, create key, test request)
- Inline help text on complex fields (virtual models, subsidy analysis, fallback chains)
- `/ui/help` page with role legend and permissions matrix
**Value**: Faster onboarding, fewer support requests

### 6. Team-Scoped Cost Visibility
**Perspective**: Product/UX
**Effort**: S (2 days) | **Impact**: High
**Problem**: Team admins can't see team-level spend. Members can't see their own costs. Only global admins see everything.
**Scope**:
- Scope `/ui/costs` by session role (GA=all, TA=team, Member=own keys)
- Team budget status widget on dashboard for TAs
- Per-member cost breakdown within team view
**Value**: Self-service cost visibility, reduces admin burden

### 7. Usage Reports & Export
**Perspective**: Product
**Effort**: M (3-5 days) | **Impact**: Medium
**Problem**: Cost data viewable in UI but can't be exported for accounting or chargeback.
**Scope**:
- CSV/JSON export of cost data (filterable by date range, team, user, key, model)
- Scheduled report generation (weekly/monthly email or webhook)
- Chargeback report: cost breakdown by team
**Value**: Finance/accounting integration, team accountability

### 8. Audit Log Enhancements
**Perspective**: Security
**Effort**: S (2-3 days) | **Impact**: Medium
**Problem**: Audit log lacks search/filter, export, and retention policies.
**Scope**:
- Filter by action type, actor, date range
- CSV export
- Configurable retention policy
- Real-time webhook for sensitive actions
**Value**: Compliance, incident investigation

---

## P2 — Medium Priority (Planned)

### 9. Request/Response Caching
**Perspective**: Architecture, Product
**Effort**: M (3-5 days)
**Problem**: Identical prompts hit upstream every time, wasting money and adding latency.
**Scope**:
- Exact-match cache (hash of model + messages + key params, temperature=0 only or opt-in)
- TTL-based expiration, max cache size
- Cache hit/miss metrics, bypass header (`X-Cache-Control: no-cache`)
**Value**: Cost reduction, faster responses for repeated queries

### 10. Self-Service Member Portal
**Perspective**: Product
**Effort**: M (3-5 days)
**Problem**: Members can't see their own usage, costs, or budget status without admin help.
**Scope**:
- "My Usage" page: personal cost breakdown, token usage, request history
- "My Keys" management: create/revoke own keys (within team budget)
- Personal budget visibility
**Value**: Reduces admin burden, user autonomy

### 11. Request Replay & Debugging Tools
**Perspective**: Product, Architecture
**Effort**: S (2-3 days)
**Problem**: No easy way to replay failed requests or export for debugging with vendors.
**Scope**:
- cURL export from log detail
- Copy request/response JSON
- "Replay" button (re-sends same request through proxy)
**Value**: Developer productivity, faster debugging

### 12. Model Routing Policies
**Perspective**: Architecture
**Effort**: L (1-2 weeks)
**Problem**: Model routing is static config. Can't route based on request characteristics.
**Scope**:
- Token-count-based routing (>100K tokens → model X)
- Time-of-day routing (off-peak → expensive model, peak → budget model)
- A/B split routing (10% to model A, 90% to model B)
**Value**: Cost optimization, experimentation

### 13. Structured Logging & Log Streaming
**Perspective**: Operations
**Effort**: S (2-3 days)
**Problem**: Logs go to file + stdout only. No aggregation service support.
**Scope**:
- Configurable log output (stdout, file, syslog, or OTLP)
- Request tracing with correlation IDs (X-Request-ID propagation)
- Configurable log level at runtime
**Value**: Ops integration, debugging in production

### 14. TLS Termination
**Perspective**: Security, Operations
**Effort**: M (3-5 days)
**Problem**: No built-in TLS. Requires reverse proxy in front.
**Scope**:
- Built-in TLS with cert/key config
- Optional ACME (Let's Encrypt)
- mTLS option for high-security deployments
**Value**: Simpler deployment, defense in depth

---

## P3 — Lower Priority (Future)

### 15. Plugin / Middleware System
Pre-request and post-response hook points. WASM or gRPC plugin interface. Built-in: PII redaction, content policy, custom headers.

### 16. Multi-Instance / HA Support
Validate PostgreSQL HA with pgbouncer. Stateless proxy layer, external session store. Load balancer compatibility.

### 17. Provider API Key Rotation
Multiple keys per provider with round-robin. Rotation without restart. Per-key usage tracking.

### 18. OpenAPI Spec & SDK Generation
OpenAPI 3.1 for all proxy endpoints. Auto-generated Python/TypeScript SDKs. Interactive API docs (Scalar or Swagger UI).

### 19. Error & Anomaly Dashboard
Top errors chart, error trends, provider health (success rate per provider), latency p50/p95/p99 trends. Auto-detection of anomalies.

### 20. Key Ownership & Bulk Management
Owner/scope columns on key table, filter by team, bulk revoke, key transfer/reassignment. CSV export of key inventory.

---

## Selected for Next Milestone

### ~~Phase 1: Security Hardening~~ ✓ COMPLETE

| Item | Effort | Status |
|------|--------|--------|
| S1. Constant-time master key comparison | XS | ✓ Done |
| S2. CSRF protection | S | ✓ Done |
| S3. CSP header | XS | ✓ Done |
| S4. Hash session tokens | S | ✓ Done |
| S5. Security package tests | M | ✓ Done |
| S6. Fix SSE stream leaks | S | ✓ Done |
| S7. Size-limit upstream responses | XS | ✓ Done |

### Phase 2: Production Readiness (2-3 weeks) ← CURRENT

| # | Feature | Effort | Impact |
|---|---------|--------|--------|
| ✓ | Prometheus Metrics | S | High — unlocks monitoring |
| 1 | API Key Scoping & Permissions | M | High — security fundamental |
| 2 | Circuit Breaker + Provider Health | M | High — reliability |
| 3 | Onboarding & Docs UI | S | High — developer experience |
| 4 | Team-Scoped Cost Visibility | S | High — self-service |

**Effort**: XS = hours, S = 1-2 days, M = 3-5 days, L = 1-2 weeks

Phase 1 makes glitchgate safe to deploy ✓. Phase 2 makes it production-grade.
