# Sprint 001: API Key Scoping & Onboarding

**Sprint Goal:** Enable per-key access controls (model allowlists, rate limits) and improve new user onboarding.

**Story Points:** 9

---

## User Stories

### US-001: Per-Key Model Allowlist
**Priority:** P0 | **Points:** 5 | **Assignee:** TBD

As an admin, I want to restrict which models an API key can access so that I can enforce least-privilege access.

**Acceptance Criteria:**
- [ ] Admin can specify allowlist of model patterns (exact + wildcard) per key
- [ ] Requests with non-allowed models return 403
- [ ] Allowlist editable via CLI and UI
- [ ] Migration adds `allowed_models` text column (JSON array)

**Technical Notes:**
- Table: `proxy_keys` → add `allowed_models` TEXT
- Modify `ProxyKeyAuthStore` to include allowed models
- Validate in middleware before model resolution

---

### US-002: Per-Key Custom Rate Limits
**Priority:** P0 | **Points:** 2 | **Assignee:** TBD

As an admin, I want to set custom rate limits per API key so that I can give different quotas.

**Acceptance Criteria:**
- [ ] Admin can set per-key `rate_limit_per_minute` and `rate_limit_burst`
- [ ] Falls back to global config if not specified per-key
- [ ] Editable via CLI and UI

**Technical Notes:**
- Add `rate_limit_per_minute` and `rate_limit_burst` columns to `proxy_keys`
- Modify rate limit middleware to check per-key config first

---

### US-012: Onboarding Documentation UI
**Priority:** P1 | **Points:** 2 | **Assignee:** TBD

As a new user, I want in-app documentation so that I can get started quickly.

**Acceptance Criteria:**
- [ ] `/ui/docs` page with endpoint reference
- [ ] Code examples: curl, Python, JavaScript
- [ ] New deployment checklist banner on dashboard
- [ ] Inline help on complex config fields

---

## Technical Tasks

1. **Database Migration**
   - Add `allowed_models`, `rate_limit_per_minute`, `rate_limit_burst` to `proxy_keys`

2. **Store Layer**
   - Update `ProxyKeyStore` interface
   - Implement in SQLite and PostgreSQL stores

3. **CLI Updates**
   - `keys create` → add `--allow-models` flag
   - `keys create` → add `--rate-limit` flags
   - `keys list` → show new columns

4. **Web UI**
   - Key create/edit form → allow models input
   - Key create/edit form → rate limit inputs
   - New `/ui/docs` page
   - Dashboard welcome banner

5. **Proxy Middleware**
   - Validate model allowlist before fallback chain execution
   - Check per-key rate limits in `KeyRateLimitMiddleware`

---

## Out of Scope (Deferred)
- Provider health dashboard (US-004) - depends on having circuit breaker
- Circuit breaker (US-005) - requires provider state tracking
- Team-scoped costs (US-009) - separate epic

---

## Success Criteria
- [ ] Admin can create a key restricted to specific models
- [ ] Restricted key gets 403 when using non-allowed model
- [ ] Admin can set custom rate limits per key
- [ ] New users see onboarding docs at `/ui/docs`
- [ ] Dashboard shows welcome banner for new deployments