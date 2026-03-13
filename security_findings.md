# Security Findings for glitchgate

**Date:** 2026-03-13  
**Validation status:** reviewed against current source and test suite  
**Scope:** application code under `cmd/`, `internal/`, schema migrations, and router configuration

---

## Executive Summary

The original draft mixed real issues with false positives and a few items that
are better described as general hardening work than security findings.

After validation against the current codebase:

- 7 findings are retained in some form
- 4 findings are materially downgraded in severity or scope
- 6 findings are removed as false positives, non-security issues, or unsupported claims

The most important confirmed risks are:

1. Proxy endpoints accept unbounded request bodies
2. The request logging pipeline can silently lose audit records under load
3. Upstream provider clients have no default request timeout
4. The application retains full request and response bodies without a log retention policy

`go test ./...` passed during validation on 2026-03-13.

---

## Confirmed Findings

### 1. No Request Body Size Limits on Proxy Endpoints

**Severity:** HIGH  
**Impact:** Authenticated denial of service, memory pressure, oversized payload abuse  
**Affected files:** `internal/proxy/handler.go`, `internal/proxy/openai_handler.go`, `internal/proxy/responses_handler.go`

All three proxy handlers read the full request body with `io.ReadAll(r.Body)`
and do not wrap the body with `http.MaxBytesReader`.

Confirmed locations:

- `internal/proxy/handler.go:69`
- `internal/proxy/openai_handler.go:49`
- `internal/proxy/responses_handler.go:49`

This is an availability issue rather than a remote unauthenticated critical
exploit, because the `/v1/*` routes require a valid proxy API key. It is still
worth fixing quickly because a valid key can submit arbitrarily large payloads.

**Recommended remediation:**

1. Enforce a maximum request size on all proxy endpoints
2. Return a consistent `413 Request Entity Too Large` style error
3. Add regression tests for oversized Anthropic, OpenAI Chat Completions, and Responses API requests

---

### 2. No Rate Limiting on Proxy or UI Authentication Paths

**Severity:** MEDIUM  
**Impact:** Brute-force pressure, resource abuse, elevated DoS risk  
**Affected files:** `cmd/serve.go`

The router currently installs `RealIP`, `Recoverer`, and `warnOnNotFound`, but
no request rate limiting middleware.

Confirmed location:

- `cmd/serve.go:168-171`

This affects both:

- authenticated proxy traffic under `/v1/*`
- login and session-establishing web routes such as `/ui/api/login`

This is a valid hardening gap, but it is not evidence of an authentication
bypass by itself.

**Recommended remediation:**

1. Add IP-based rate limits for login routes
2. Add proxy-key-based rate limits for `/v1/*`
3. Add basic global concurrency and burst controls
4. Emit metrics and logs when rate limits trigger

---

### 3. Async Logger Can Drop Audit Records Under Load

**Severity:** MEDIUM  
**Impact:** Incomplete audit trail, missing billing/security records  
**Affected files:** `internal/proxy/logger.go`

The async logger drops entries when its channel is full:

- `internal/proxy/logger.go:31-36`

The code logs a warning, but there is no metric, alert threshold, or fallback
path.

This is a real issue. The original report overstated the compliance angle, but
the core finding stands: sustained load can cause request logs to disappear.

**Recommended remediation:**

1. Count dropped entries with a metric
2. Add bounded backpressure before dropping
3. Make buffer size and drop behavior configurable
4. Consider a durable overflow strategy if audit completeness is a requirement

---

### 4. Async Logger Uses `context.Background()` for Database Writes

**Severity:** MEDIUM  
**Impact:** Stalled log writer, secondary log loss during DB degradation  
**Affected files:** `internal/proxy/logger.go`

The async logger writes with an unbounded background context:

- `internal/proxy/logger.go:48`

If the database blocks, the single logger goroutine can stall indefinitely,
causing the buffer to fill and increasing the chance of dropped entries.

The original report’s “connection exhaustion” framing was too strong. The more
direct risk is that logging degrades badly when SQLite becomes slow or wedged.

**Recommended remediation:**

1. Use `context.WithTimeout` for log inserts
2. Record timeout failures separately from ordinary insert errors
3. Add tests for slow or blocked store writes

---

### 5. Upstream Provider Clients Have No Default Request Timeout

**Severity:** MEDIUM  
**Impact:** Hung upstream requests can tie up server resources indefinitely  
**Affected files:** `internal/provider/anthropic/client.go`, `internal/provider/openai/client.go`, `internal/provider/copilot/client.go`

Each provider client constructs a default `http.Client` with no `Timeout`:

- `internal/provider/anthropic/client.go:35`
- `internal/provider/openai/client.go:39`
- `internal/provider/copilot/client.go:35`

The request context is propagated, which is good, but there is no guarantee the
caller supplied a deadline. That leaves a path for indefinite upstream hangs.

**Recommended remediation:**

1. Set a sane default client timeout
2. Preserve support for per-request context deadlines
3. Add configuration if different providers need different timeout envelopes
4. Add tests covering timeout behavior for streaming and non-streaming requests

---

### 6. Full Request and Response Bodies Are Stored Without a Retention Policy

**Severity:** MEDIUM  
**Impact:** Privacy exposure, storage growth, larger blast radius for database compromise  
**Affected files:** `internal/store/migrations/002_create_request_logs.sql`, `internal/store/sqlite.go`

The `request_logs` table stores full request and response bodies as `TEXT`:

- `internal/store/migrations/002_create_request_logs.sql:14-15`

The application also exposes these fields in detailed log views:

- `internal/store/sqlite.go:359-409`

The codebase does redact some obvious secrets from request bodies before
logging, but there is no retention window or automated purge for request logs.

This is primarily a data-retention and privacy concern. The original report
described these fields as BLOBs, which is incorrect for the current schema.

**Recommended remediation:**

1. Define a retention policy for `request_logs`
2. Add a pruning job and operator-configurable retention duration
3. Consider truncation limits for stored bodies
4. Review whether additional sensitive fields should be redacted before storage

---

### 7. Missing Baseline HTTP Security Headers

**Severity:** LOW  
**Impact:** Weaker browser-side security posture  
**Affected files:** `cmd/serve.go`, `internal/web/templates/*.html`

I did not find middleware that sets baseline headers such as:

- `X-Content-Type-Options: nosniff`
- `X-Frame-Options` or equivalent `frame-ancestors`
- `Permissions-Policy`

The application does set secure session cookie attributes, including
`HttpOnly`, `Secure`, and `SameSite=Lax`.

Confirmed cookie usage:

- `internal/web/handlers.go:284-291`
- `internal/web/auth_handlers.go:132-139`

This is a valid hardening item, but the original report’s severity was too
high. A strict Content Security Policy also requires extra work because the UI
currently uses inline scripts.

**Recommended remediation:**

1. Add `X-Content-Type-Options: nosniff`
2. Add `X-Frame-Options: DENY` or a CSP `frame-ancestors 'none'`
3. Add `Referrer-Policy`
4. Introduce CSP only after inventorying current inline script usage

---

## Findings Removed or Reclassified

### Removed as False Positive or Unsupported

1. **Insufficient input validation on login causing authentication bypass**
   - Not supported.
   - `master_key` is required at config load, so the empty-string bypass path in
     the draft is not reachable in a valid startup configuration.
   - Relevant code: `internal/config/config.go:145-147`, `internal/web/handlers.go:248-259`

2. **Session token generation uses default rand**
   - False positive.
   - The code correctly uses `crypto/rand`.
   - Relevant code: `internal/auth/session.go:35-39`

3. **SQL query construction with string concatenation**
   - Not a current vulnerability.
   - Sort columns are chosen from an allowlist and values are still bound with
     placeholders.
   - Relevant code: `internal/store/sqlite.go:254-322`

4. **Error messages leak internal details**
   - Unsupported by the cited examples.
   - The reviewed external error messages are generic and do not expose stack
     traces, SQL, filesystem paths, or upstream secrets.

5. **No distributed tracing**
   - Not a security finding.
   - This is an observability enhancement, not a vulnerability.

### Reclassified as Lower-Severity Hardening Work

1. **Potential race condition in streaming translation**
   - Rejected as a race condition.
   - The code writes to the captured buffer and client sequentially, not
     concurrently.
   - There may be a small bookkeeping mismatch on partial client failure, but it
     is not a high-severity security issue.
   - Relevant code: `internal/translate/responses_stream_translator.go:602-614`

2. **Unchecked query parameters**
   - Not a security issue as written.
   - `parseLogParams` is permissive, but the store layer clamps `perPage` to a
     safe range before query execution.
   - Relevant code: `internal/web/handlers.go:807-833`, `internal/store/sqlite.go:325-333`

3. **Database initialization has no explicit context timeout**
   - Best described as resilience work, not a meaningful security issue on its own.
   - Relevant code: `internal/store/sqlite.go:33-45`

4. **Missing CSRF protection**
   - Partially mitigated today by `SameSite=Lax` cookies and state-changing
     routes using POST/DELETE rather than GET.
   - Still worth addressing as defense in depth, but not strong enough to keep
     as a primary validated finding without a demonstrated bypass.

---

## Positive Security Controls Confirmed

The following controls are present in the current codebase:

1. **Proxy API keys are bcrypt-hashed at rest**
   - `internal/auth/keys.go:12-18`, `internal/auth/keys.go:41-49`

2. **UI sessions use cryptographically secure random tokens**
   - `internal/auth/session.go:35-39`

3. **Session cookies use secure attributes**
   - `HttpOnly`, `Secure`, `SameSite=Lax`
   - `internal/web/handlers.go:284-291`, `internal/web/auth_handlers.go:132-139`

4. **Proxy API key authentication is enforced on `/v1/*`**
   - `cmd/serve.go:174-178`, `internal/proxy/middleware.go:29-70`

5. **OIDC login uses random state and PKCE**
   - `internal/oidc/state.go:12-34`

6. **SQL value inputs are parameterized**
   - Query building around sort and scope uses allowlists and placeholders

7. **Some request-body redaction is already implemented**
   - `internal/proxy/redact.go`

---

## Prioritized Remediation Order

### Immediate

1. Add request body limits to all proxy handlers
2. Add upstream HTTP client timeouts
3. Put timeouts and metrics around async log persistence
4. Add metrics/alerts for dropped log entries

### Near Term

5. Add rate limiting for login and proxy traffic
6. Define and implement request log retention
7. Add baseline security headers middleware

### Later Hardening

8. Revisit CSRF protections for state-changing UI routes
9. Expand redaction coverage for sensitive fields in logged payloads
10. Add resilience tests for slow upstreams and slow SQLite writes

---

## Related Document

See `docs/security-hardening-plan.md` for a phased implementation plan.
