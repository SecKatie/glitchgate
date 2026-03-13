# Security Hardening Plan for glitchgate

**Date:** 2026-03-13  
**Input:** validated findings in `security_findings.md`  
**Goal:** reduce availability, privacy, and browser-facing security risk without
disrupting core proxy behavior

---

## Principles

1. Fix resource-exhaustion paths before polishing browser hardening
2. Prefer narrow, testable changes over broad framework additions
3. Add observability with each control so operators can verify it works
4. Preserve current API behavior unless the change intentionally adds a limit
5. Treat logging and retention changes as data-governance work, not only app security work

---

## Workstreams

### A. Resource Protection

Scope:

- request size limits
- rate limiting
- upstream timeouts
- bounded logging behavior

Primary files:

- `cmd/serve.go`
- `internal/proxy/handler.go`
- `internal/proxy/openai_handler.go`
- `internal/proxy/responses_handler.go`
- `internal/provider/anthropic/client.go`
- `internal/provider/openai/client.go`
- `internal/provider/copilot/client.go`
- `internal/proxy/logger.go`

### B. Logging and Data Governance

Scope:

- dropped-log visibility
- timeout handling around log persistence
- retention and pruning
- redaction review

Primary files:

- `internal/proxy/logger.go`
- `internal/proxy/redact.go`
- `internal/store/sqlite.go`
- `internal/store/migrations/*`
- `cmd/serve.go`

### C. Web Security Posture

Scope:

- baseline security headers
- CSRF posture
- browser-side trust boundaries

Primary files:

- `cmd/serve.go`
- `internal/web/middleware.go`
- `internal/web/templates/*.html`

### D. Verification and Operationalization

Scope:

- tests
- documentation
- rollout controls
- operator visibility

Primary files:

- `internal/proxy/*_test.go`
- `internal/provider/*_test.go`
- `internal/web/*_test.go`
- `docs/configuration.md`
- `README.md`

---

## Phase 1: Immediate Hardening

**Target window:** 1 week  
**Outcome:** close the highest-value availability gaps

### 1. Add Request Size Limits to Proxy Routes

Tasks:

1. Introduce a shared max body size constant for proxied requests
2. Wrap request bodies with `http.MaxBytesReader` in all three proxy handlers
3. Return a stable error for oversized requests
4. Add tests covering oversized Anthropic, OpenAI, and Responses requests

Acceptance criteria:

- requests above the configured limit are rejected before full body allocation
- normal requests continue to behave unchanged
- tests cover all three route families

### 2. Add Default Upstream Timeouts

Tasks:

1. Add a default timeout to each provider `http.Client`
2. Ensure streaming behavior remains compatible with long-lived responses
3. Decide whether streaming and non-streaming routes need distinct timeout settings
4. Document operator-tunable values if configuration is added

Acceptance criteria:

- upstream hangs terminate predictably
- timeouts surface as controlled proxy errors
- tests validate timeout handling

### 3. Bound Async Logger Writes

Tasks:

1. Replace `context.Background()` in async log persistence with a bounded timeout
2. Differentiate insert timeout failures from ordinary DB errors
3. Add tests for slow store writes

Acceptance criteria:

- log writer cannot block forever on one insert
- timeout failures are visible in logs/metrics

### 4. Add Visibility for Dropped Log Entries

Tasks:

1. Introduce counters for enqueued, persisted, failed, and dropped entries
2. Emit warnings with rate-limited logging if drops spike
3. Decide whether brief backpressure should be attempted before dropping

Acceptance criteria:

- operators can tell when audit loss is occurring
- production logs are not flooded by repetitive warnings

---

## Phase 2: Short-Term Hardening

**Target window:** 30 days  
**Outcome:** improve abuse resistance and browser posture

### 5. Add Rate Limiting

Tasks:

1. Add IP-based rate limiting to `/ui/api/login`
2. Add proxy-key-based rate limiting for `/v1/*`
3. Add optional IP-based fallback limits for unauthenticated abuse
4. Expose rate-limit configuration in app config

Design notes:

- login limits should be strict and low burst
- proxy limits should be tied to expected tenant traffic
- rate limiting should fail closed only when limits are explicitly exceeded, not when backing storage for counters is unavailable

Acceptance criteria:

- repeated login attempts from one IP are throttled
- one noisy proxy key cannot monopolize service capacity
- rate-limit hits are observable

### 6. Add Baseline Security Headers

Tasks:

1. Add a middleware for `X-Content-Type-Options: nosniff`
2. Add `X-Frame-Options: DENY` or equivalent CSP `frame-ancestors`
3. Add `Referrer-Policy`
4. Add `Permissions-Policy` with a narrow default

Design notes:

- do not add strict CSP until inline scripts are inventoried and refactored as needed

Acceptance criteria:

- all HTML UI responses include the baseline header set
- headers do not break current navigation or HTMX behavior

### 7. Review CSRF Posture

Tasks:

1. Inventory all authenticated state-changing UI routes
2. Decide whether SameSite=Lax is sufficient for current threat model
3. If not, add CSRF tokens for form and fetch-based mutations
4. Update templates and JS helpers consistently

Acceptance criteria:

- the team has an explicit CSRF model rather than relying on implicit cookie behavior
- if tokens are added, all state-changing routes enforce them

---

## Phase 3: Data Governance and Retention

**Target window:** 60-90 days  
**Outcome:** reduce long-term privacy and storage risk from request logging

### 8. Define Log Retention Policy

Tasks:

1. Decide default retention duration for `request_logs`
2. Document whether operators can disable or extend retention
3. Define expectations for audit use, debugging use, and privacy boundaries

Acceptance criteria:

- a documented retention policy exists
- defaults are reasonable for a self-hosted deployment

### 9. Implement Log Pruning

Tasks:

1. Add store methods to delete old log rows in batches
2. Add a background pruning job
3. Add config for retention duration and prune cadence
4. Add tests for retention cutoff behavior

Acceptance criteria:

- old rows are removed automatically
- pruning is safe on large tables and does not lock the app for long periods

### 10. Tighten Redaction and Storage Boundaries

Tasks:

1. Review currently redacted fields and payload shapes
2. Add truncation or byte limits for stored bodies
3. Consider hashing or redacting additional high-risk fields
4. Document exactly what is retained in logs

Acceptance criteria:

- secret handling rules are explicit
- large sensitive payloads do not remain fully stored by default

---

## Phase 4: Verification and Sustainment

**Target window:** ongoing  
**Outcome:** keep hardening changes from regressing

### 11. Expand Test Coverage

Add tests for:

1. oversized request rejection on each proxy route
2. upstream timeout behavior
3. slow/dropped async log persistence
4. rate limit enforcement
5. security header presence
6. retention pruning behavior

### 12. Add Operator Documentation

Update docs to cover:

1. request size limits
2. rate-limit configuration
3. timeout defaults
4. retention and pruning behavior
5. security header policy

### 13. Add Lightweight Security Review Cadence

Recommended cadence:

1. run `make audit` in CI or before releases
2. review request logging/redaction changes during code review
3. re-run a focused security review when auth, proxy, or logging architecture changes

---

## Suggested Delivery Order

If this is executed as implementation work, the recommended order is:

1. proxy request size limits
2. upstream client timeouts
3. async logger timeout and drop metrics
4. login and proxy rate limiting
5. baseline security headers
6. retention policy plus pruning
7. CSRF decision and implementation if needed
8. redaction and truncation refinements

This order reduces operational risk first, then moves into data governance and
browser hardening.

---

## Suggested Issue Breakdown

### Epic 1: Proxy Resilience

Stories:

1. Enforce max request body size on all proxy routes
2. Add provider client timeouts
3. Add tests for oversized and timed-out upstream requests

### Epic 2: Logging Reliability

Stories:

1. Bound async log insert duration
2. Expose dropped-log metrics
3. Decide and implement overflow behavior

### Epic 3: Abuse Controls

Stories:

1. Rate-limit login endpoints
2. Rate-limit proxy traffic by key
3. Add observability for rate-limit events

### Epic 4: Data Retention and Privacy

Stories:

1. Define retention defaults
2. Implement pruning job
3. Review redaction and truncation strategy

### Epic 5: Browser Security

Stories:

1. Add baseline response headers
2. Decide CSRF strategy
3. Refactor inline scripts if strict CSP becomes a goal

---

## Exit Criteria

This hardening plan can be considered complete when:

1. proxy routes reject oversized bodies
2. upstream hangs fail within bounded time
3. dropped logs are measurable and rare
4. old request logs are pruned automatically
5. baseline browser security headers are present
6. rate limiting is active on login and proxy paths
7. documentation reflects the actual hardening controls in the product
