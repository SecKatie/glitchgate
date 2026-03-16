# Readiness Gaps

Assessment date: 2026-03-15 (updated)

## Status

`glitchgate` is not ready for production deployment in its current form.

The codebase has solid structure, good test coverage in core paths, working containerization, and strong security fundamentals (bcrypt key hashing, OIDC with PKCE, parameterized SQL, rate limiting, RBAC). However, there are release-blocking gaps across authorization, budget enforcement, CI, and operational visibility.

Release-blocking gaps:

1. ~~A web UI authorization flaw allows scoped users to discover and modify keys they should not control.~~ **FIXED** (2026-03-15).
2. ~~Budget enforcement is described as a shipped feature, but is not enforced on the proxy request path.~~ **FIXED** (2026-03-15). P1 (enforcement), P2 (dashboard display), and P3 (management UI) complete.
3. ~~No CI pipeline exists -- lint, test, and security scanning are not automated.~~ **FIXED** (2026-03-15).
4. ~~No version embedding -- deployed builds cannot be identified.~~ **FIXED** (2026-03-15).
5. Security-critical packages have zero test coverage.

## What Was Verified

The following checks were run successfully:

- `go test -race ./...` -- all tests pass, no races detected
- `go build ./...`
- `gosec ./...` -- 0 issues (23 `#nosec` annotations, all justified)
- `govulncheck ./...` -- 0 known vulnerabilities
- `golangci-lint run` -- 0 issues

## Release-Blocking Gaps

### ~~1. Proxy key authorization is incomplete in the UI~~ FIXED

Resolved 2026-03-15. Changes:

- `UpdateKeyLabelHandler` now enforces scope via `canMutateKey` before allowing updates (returns 403 for unauthorized keys).
- `CreateKeyHandler` uses `listKeysForSession` instead of unscoped `ListActiveProxyKeys` on validation error.
- `buildScopeParams` edge cases (team_admin without team/user, member without user) now return `"none"` instead of `"all"`.
- `listKeysForSession` default branch returns empty slice instead of all keys.
- `RevokeKeyHandler` refactored to use shared `canMutateKey` helper.
- 15 new tests cover scope enforcement for update, revoke, listing, and edge cases across all role types.

### ~~2. Budget enforcement is not implemented on the serving path~~ FIXED

Resolved 2026-03-15. Changes:

- Added `cost_usd` column to `request_logs` (migration 023) with covering index for budget queries.
- Cost is computed at log time via `pricing.Calculator` and stored alongside token counts.
- `BudgetChecker` performs pre-flight checks before upstream dispatch in all three handlers (Anthropic, OpenAI, Responses).
- `GetApplicableBudgets` queries all 4 budget tables (key, user, team, global) via UNION ALL.
- `GetSpendSince` sums `cost_usd` for each scope since the period start.
- Period boundaries (daily/weekly/monthly) are timezone-aware using the configured display timezone.
- Budget check fails open on DB errors (logs warning, allows request).
- Returns 429 with scope, period, limit, spend, and reset time on violation.
- 30 new tests cover period calculation, budget checker logic, and store queries.

**P2 (budget status dashboard)**: Budget utilization displayed on cost dashboard with progress bars, percentage used, and color-coded status indicators (ok/warning/exceeded). Includes reset time display.

**P3 (budget management UI)**: Web UI for administrators to set/update/clear budget limits at global, user, and team scopes. Inline edit forms on budget status cards with HTMX. GA sees all scopes with edit controls and "Set new budget" form; TA sees team budget with edit; members see read-only status. Input validation enforces positive limits with max 2 decimal places and valid periods. All mutations are audit-logged.

### ~~3. No CI pipeline~~ FIXED

Resolved 2026-03-15. Changes:

- Added `.github/workflows/ci.yml` with 4 parallel jobs: lint, test, audit, build.
- Lint uses `golangci/golangci-lint-action` matching local `.golangci.yml`.
- Test runs `go test -race ./...` via `make test`.
- Audit installs and runs `gosec` (SAST) and `govulncheck` (SCA).
- Build verifies `CGO_ENABLED=0 go build` succeeds.
- All jobs trigger on push and pull_request to any branch.
- Go version auto-tracked from `go.mod` via `go-version-file`.

### ~~4. No version embedding~~ FIXED

Resolved 2026-03-15. Changes:

- Added `cmd/version.go` with `version`, `commit`, `date` vars injected via ldflags.
- `version` subcommand prints `glitchgate <version> (commit: <sha>, built: <date>)`.
- `rootCmd.Version` set so `--version` flag works automatically.
- `.goreleaser.yaml` ldflags inject `{{.Version}}`, `{{.Commit}}`, `{{.Date}}`.
- `Makefile` build target injects version from `git describe`, commit, and date.
- `Containerfile` accepts `VERSION` ARG and injects via ldflags + OCI label.
- Also fixes gap #13: `GOARCH` now uses `TARGETARCH` ARG for multi-arch builds.

### 5. Zero test coverage for security-critical packages

Severity: High

Why this blocks deployment:

- `internal/auth/` -- key generation, bcrypt hashing, session management -- has no test files.
- `internal/oidc/` -- OIDC provider and state management -- has no test files.
- `internal/ratelimit/` -- rate limiter -- has no test files.
- `internal/provider/copilot/` -- Copilot OAuth flow -- has no test files.

Impact:

- Security-critical behavior is unverified by automated tests.
- Refactoring these packages carries high regression risk.
- `internal/pricing` at 47% coverage is also concerning for a cost-tracking system.

Required fix before release:

- Add unit tests for key generation, hashing, and verification in `internal/auth/`.
- Add unit tests for OIDC state creation, consumption, and expiry in `internal/oidc/`.
- Add unit tests for rate limiter behavior, cleanup, and edge cases in `internal/ratelimit/`.
- Increase `internal/pricing` coverage to cover all supported model lookups and edge cases.

## Important Non-Blocking Gaps

These do not necessarily block a small internal deployment, but they should be treated as serious hardening work.

### 6. Master key comparison is timing-unsafe

Severity: Medium

Evidence:

- Master key login uses `!=` comparison (`internal/web/handlers.go` line 289).
- No import of `crypto/subtle` exists in this file.

Impact:

- A timing side-channel could allow statistical recovery of the master key over many attempts, even with rate limiting.

Recommended fix:

- Replace with `subtle.ConstantTimeCompare([]byte(masterKey), []byte(h.masterKey))`.

### 7. No CSRF protection on state-mutating endpoints

Severity: Medium

Evidence:

- No CSRF token generation or validation exists anywhere in the codebase.
- POST endpoints for key creation, revocation, label updates, user role changes, and deactivation have no CSRF defense beyond `SameSite=Lax` cookies.

Impact:

- `SameSite=Lax` does not protect against top-level navigation POST attacks.
- A malicious page could trigger state changes if a user is logged in.

Recommended fix:

- Add CSRF token validation (per-session or double-submit cookie) for all state-mutating POST endpoints.

### 8. No Content-Security-Policy header

Severity: Medium

Evidence:

- `SecurityHeadersMiddleware` (`internal/web/security_middleware.go`) sets `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, and `Permissions-Policy` but not `Content-Security-Policy`.

Impact:

- No defense-in-depth against XSS.
- The UI uses HTMX and inline scripts that would benefit from a scoped CSP.

Recommended fix:

- Add a CSP header. Start with `default-src 'self'; script-src 'self' 'unsafe-inline'` and tighten with nonces over time.

### 9. Session tokens are stored in plaintext

Severity: Medium

Evidence:

- UI session tokens are stored directly in the `ui_sessions` table (`internal/store/ui_sessions.go` line 21).
- Sessions are looked up by raw token value (`internal/store/ui_sessions.go` line 30).

Impact:

- Anyone with read access to the database can reuse active sessions immediately.
- This is weaker than the proxy key design, which stores a bcrypt hash.

Recommended fix:

- Store only a hash of the session token.
- Compare hashed tokens on lookup.
- Consider session rotation on sensitive actions.

### 10. Request and response logging carries sensitive-data risk

Severity: Medium

Evidence:

- Full request and response bodies are persisted in `internal/proxy/result.go` lines 43-64.
- Request redaction in `internal/proxy/redact.go` only recurses through nested maps, not arrays, and returns raw bodies on parse failure.

Impact:

- Prompt content, model outputs, or tool payloads may be written to disk.
- Some secrets embedded in arrays or non-JSON payloads may bypass redaction.
- This increases the blast radius of database or log access.

Recommended fix:

- Decide whether full-body logging is truly required for production.
- Add recursive redaction for arrays and mixed JSON structures.
- Consider opt-in body logging, field allowlists, or environment-based redaction modes.

### 11. SSE translate streams leak upstream connections on client disconnect

Severity: Medium

Evidence:

- `RelaySSEStream` (`internal/proxy/stream.go`) correctly uses `closeOnCancel(ctx, upstream)` to close the upstream reader when the client disconnects.
- `ReverseSSEStream` (`internal/translate/reverse_stream.go`) and `ResponsesSSEToAnthropicSSE` (`internal/translate/responses_reverse_stream.go`) do NOT accept a context and do NOT close upstream on cancellation.

Impact:

- If clients disconnect mid-stream (common with long LLM requests), upstream connections remain open until the provider finishes generating.
- Under load with frequent disconnects, upstream connections accumulate, wasting resources.

Recommended fix:

- Thread context through translate stream functions.
- Add `closeOnCancel` or equivalent cleanup.

### 12. No observability infrastructure

Severity: Medium

Evidence:

- No Prometheus `/metrics` endpoint or similar.
- No OpenTelemetry or distributed tracing integration.
- Health check (`/health`) returns static "ok" without verifying DB connectivity.
- Log level is hardcoded to `slog.LevelInfo` (`cmd/serve.go` line 52) with no config option.

Impact:

- Cannot integrate with monitoring infrastructure (Grafana, Datadog, etc.).
- Cannot enable debug logging without code changes.
- Degraded states (DB down, upstream unreachable) are not surfaced by the health endpoint.

Recommended fix:

- Add a configurable log level.
- Add a `/metrics` endpoint with request counters, latency histograms, and token usage.
- Make health check verify DB connectivity.

### ~~13. Containerfile hardcodes GOARCH=amd64~~ FIXED

Resolved 2026-03-15 as part of gap #4 (version embedding). The Containerfile now uses `ARG TARGETARCH` with `GOARCH=${TARGETARCH:-amd64}`.

### 14. SQLite PRAGMAs not applied per-connection

Severity: Medium

Evidence:

- PRAGMAs (`foreign_keys=ON`, `busy_timeout=5000`) are executed once against `*sql.DB` (`internal/store/sqlite.go` lines 41-57).
- Go's `database/sql` may open new connections in the pool that do not inherit per-connection PRAGMAs.

Impact:

- New pooled connections may lack `foreign_keys=ON` or `busy_timeout`, causing silent referential integrity failures or immediate SQLITE_BUSY errors under contention.

Recommended fix:

- Use a connection init hook or DSN `_pragma` parameter to apply PRAGMAs on every new connection.

### 15. Scale is limited to a single-node SQLite deployment

Severity: Medium

Evidence:

- The application is built directly on local SQLite (`internal/store/sqlite.go` lines 28-64).
- Connection count is intentionally capped at four (`internal/store/sqlite.go` lines 59-62).

Impact:

- Horizontal scaling is not available.
- Write-heavy workloads will contend on the database.

Recommended fix:

- Be explicit that the supported deployment model is single-instance.
- Define expected request volume and retention limits.

### 16. Async logging can drop audit and accounting records under pressure

Severity: Medium

Evidence:

- The async logger uses a 100ms enqueue timeout and drops entries when the buffer is full (`internal/proxy/logger.go` lines 95-114).

Impact:

- Request history can become incomplete during spikes or storage slowdowns.
- Cost reporting and incident investigation become less trustworthy.

Recommended fix:

- Decide whether dropping logs is acceptable for this product.
- If not, move to backpressure or durable queueing.
- Add operational metrics for dropped, timed-out, and failed log writes.

## Low-Severity Items

These are worth addressing but unlikely to cause incidents on their own.

- **OIDC error parameter reflected unsanitized** -- `internal/web/auth_handlers.go` line 88 reflects the IDP `error` query param directly. Sanitize to known OIDC error codes.
- **No log rotation** -- log file opened with `O_APPEND` and never rotated. Less critical in containers (stdout capture), but the file at `log_path` grows unbounded.
- **Upstream response bodies not size-limited** -- providers use `io.ReadAll(resp.Body)` with no `io.LimitReader` for non-streaming responses.
- **Unbounded `bytes.Buffer` for captured stream data** -- all stream relay functions capture full streams in memory with no size limit.
- **Rate limiter mutex contention** -- single global `sync.Mutex` with in-memory-only state. Fine for single-instance but cleanup inside `Allow()` scans all entries while holding the lock.
- **`MaxIdleConnsPerHost` defaults to 2** -- poor HTTP connection reuse for high-throughput single-provider deployments.
- **`go mod tidy` needed** -- three direct dependencies (`anthropic-sdk-go`, `openai-go`, `x/sync`) are misclassified as indirect.

## Maintainability Assessment

Overall maintainability is good.

Strengths:

- Clear package boundaries with well-decomposed store interfaces.
- 0 lint issues, 0 race conditions, consistent error wrapping with `fmt.Errorf`.
- Good automated test coverage in proxy, store, translation, and web layers.
- Container, compose, and goreleaser assets are present.
- Security headers, request size limits, and rate limits are already in place.
- Excellent documentation (CLAUDE.md, architecture.md, config.example.yaml).

Current maintainability risks:

- Product claims (README) and actual enforcement behavior are out of sync (budget enforcement).
- Authorization rules are implemented partly in handlers and partly by convention, making drift easier.
- Security-critical packages (auth, oidc, ratelimit) have zero test coverage, making refactoring risky.
- ~~No CI pipeline means quality gates depend entirely on developer discipline.~~ CI added.
- The logging model couples observability, cost reporting, and auditability to a best-effort async path.

## Deployment Criteria

The app should not be called deployment-ready until all items below are complete:

- [x] Fix scoped key authorization for list, update, and all related UI paths.
- [x] Implement real budget enforcement on the proxy path, or remove claims from README.
- [x] Add a CI pipeline running lint, test, and security audit on every push.
- [x] Embed version information in binaries and container images.
- [ ] Add tests for `internal/auth`, `internal/oidc`, and `internal/ratelimit`.
- [ ] Fix constant-time master key comparison.
- [x] Fix Containerfile `GOARCH` for multi-arch builds.
- [ ] Decide and document the supported deployment model.
- [ ] Decide and document production logging posture for prompts, responses, and session data.

## Suggested Remediation Order

1. ~~Fix key-scope authorization bugs and add regression tests.~~ **Done.**
2. ~~Add CI pipeline (`make lint test audit`).~~ **Done.**
3. ~~Embed version information (goreleaser ldflags + cobra subcommand).~~ **Done.**
4. ~~Fix Containerfile `GOARCH` hardcoding.~~ **Done (with #3).**
5. Fix constant-time master key comparison.
6. Add tests for auth, oidc, and ratelimit packages.
7. ~~Implement budget enforcement or remove claims from README.~~ **Done (P1/P2/P3).**
8. Add CSRF tokens and CSP header.
9. Fix SSE translate stream connection leaks.
10. Add configurable log level and metrics endpoint.
11. Harden session storage and request/response redaction.

## Bottom Line

This codebase has a strong foundation -- clean architecture, good security fundamentals, and well-organized code. It is closer to production-ready than most projects at this stage.

It is currently:

- Secure for limited internal use with careful operator trust assumptions, after fixing the master key timing issue.
- Not fully secure for a multi-user deployment until CSRF, CSP, and session hardening are addressed.
- Scalable only as a single-instance service with modest traffic.
- Maintainable and well-organized, but needs CI and test coverage in critical packages to stay that way.
- Not ready for general deployment until the release blockers above are fixed.
