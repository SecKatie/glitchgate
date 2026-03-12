# Research: OIDC Authentication for User & Team Management

**Branch**: `007-implement-oidc` | **Date**: 2026-03-11

All unknowns were resolved via spec clarifications before plan generation.
This document records the decisions and rationale for each topic area.

---

## 1. OIDC Library: `go-oidc/v3` + `x/oauth2`

**Decision**: `github.com/coreos/go-oidc/v3` + `golang.org/x/oauth2`

**Rationale**:
- `go-oidc/v3` provides a production-grade `IDTokenVerifier` that handles JWKS
  fetching (with caching), ID token parsing, and claim validation (`iss`, `aud`,
  `exp`, nonce). It does not replace `x/oauth2` — the two libraries compose.
- `x/oauth2` handles the authorization code exchange and natively supports PKCE
  via `oauth2.GenerateVerifier()` + `oauth2.S256ChallengeOption()`.
- This is the dominant combination in the Go ecosystem (used by Dex, Pomerium,
  and most Go OIDC proxies).

**PKCE integration pattern**:
```go
// 1. Generate verifier + challenge
verifier := oauth2.GenerateVerifier()
challenge := oauth2.S256ChallengeOption(verifier)

// 2. Build auth URL with challenge
authURL := cfg.AuthCodeURL(state, challenge)

// 3. Exchange code with verifier
token, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))

// 4. Verify ID token
rawIDToken := token.Extra("id_token").(string)
idToken, err := verifier.Verify(ctx, rawIDToken)
```

**Alternatives considered**:
- `github.com/zitadel/oidc`: more opinionated and heavier; designed for building
  OIDC *providers*, not just clients. Overkill for a consumer-side implementation.
- Hand-rolled `net/http`: would require manual JWKS fetching, key rotation,
  claim validation. Security risk; rejected.

**New go.mod dependencies**:
```
golang.org/x/oauth2
github.com/coreos/go-oidc/v3
```

---

## 2. Session Storage: SQLite-backed server-side sessions

**Decision**: Replace the existing in-memory `auth.SessionStore` with a
DB-backed `ui_sessions` table. Browser receives a random 32-byte hex token
in an `HttpOnly, Secure, SameSite=Lax` cookie named `llmp_session`.

**Rationale**:
- The spec requires immediate session revocation on deactivation (SC-003, FR-013).
  An in-memory store cannot be shared across process restarts or future multi-instance
  deployments; a DB-backed store handles both.
- The existing `auth.SessionStore` already uses a random token; migrating to DB
  changes only the backing store, not the cookie contract.
- SQLite session lookup is a single indexed read (`WHERE token = ?` on a unique
  index) — negligible overhead vs in-memory map under typical load.

**Session table schema** (see `data-model.md` for full DDL):
- `id` TEXT PK (UUID), `token` TEXT UNIQUE, `session_type` (oidc|master_key),
  `user_id` TEXT nullable (NULL for master_key sessions), `created_at`, `expires_at`.

**Expiry**: 8 hours (hardcoded for this iteration; not operator-configurable).
A background cleanup goroutine deletes expired rows on startup and every hour.

**Alternatives considered**:
- Keep in-memory store: cannot revoke on deactivation without a DB lookup anyway
  (to check if the user is still active), so DB sessions are strictly better.
- JWT session tokens: stateless but cannot be revoked without a denylist —
  which is just a DB table anyway. Rejected.

---

## 3. OIDC State Parameter + CSRF Protection

**Decision**: Store `(state, pkce_verifier, redirect_to, expires_at)` in a
short-lived `oidc_state` SQLite table. State entries expire after 10 minutes
and are deleted on use (one-time consumption).

**Rationale**:
- The `state` parameter must be validated on callback to prevent CSRF attacks.
  Storing it server-side (vs a signed cookie) avoids any client-visible state.
- Using DB (vs in-memory map) means the state survives a restart within the
  10-minute window and works correctly under future load-balanced deployments.
- The `pkce_verifier` must accompany the state because the callback handler
  needs it to complete the token exchange — same row is natural.

**Cleanup**: expired `oidc_state` rows are deleted at startup and every 30 minutes
by the same cleanup goroutine that handles `ui_sessions`.

**Alternatives considered**:
- Signed `state` cookie: encodes state client-side; avoids DB but leaks the
  pkce_verifier to the client cookie store. Rejected on principle of minimal
  client-side exposure.

---

## 4. Role Model & Authorization Middleware

**Decision**: Three roles stored as a `CHECK` constraint in `oidc_users.role`:
`'global_admin'`, `'team_admin'`, `'member'`. Authorization enforced by
chi middleware functions loaded from session context.

**Middleware chain** (new `internal/web/middleware.go` additions):
- `UISessionMiddleware`: replaces `web.SessionMiddleware`; loads `ui_session`
  from DB by cookie token, loads associated `oidc_user` if OIDC session, injects
  into `context.Context`. Redirects to `/ui/login` if no valid session.
- `RequireGlobalAdmin`: gate — 403 if role ≠ `global_admin` (and not master_key session).
- `RequireAdminOrTeamAdmin`: gate — allows `global_admin` or `team_admin`.

**Master key session**: stored as a `ui_sessions` row with `session_type = 'master_key'`
and `user_id = NULL`. Middleware sets a `isMasterKey = true` flag in context;
all authorization checks treat this as `global_admin` equivalent.

---

## 5. Key Ownership Migration

**Decision**: Add `owner_user_id TEXT REFERENCES oidc_users(id)` (nullable) to
`proxy_keys`. Legacy keys (created before OIDC was enabled) have `owner_user_id = NULL`
and are visible only to Global Admins and master_key sessions.

**Key creation flow**: The `POST /ui/api/keys` handler now associates the created
key with the authenticated OIDC user's `id`. Master key sessions create unowned
(legacy) keys.

**Rationale**: User-owned keys allow per-user log scoping (`WHERE proxy_key_id IN
(SELECT id FROM proxy_keys WHERE owner_user_id = ?)`) without changing the
request-path proxy auth at all.

---

## 6. Log Scoping Query Strategy

**Decision**: Extend `ListLogsParams` with a `ScopeUserID` and `ScopeTeamID`
field. The SQLite query applies a `WHERE proxy_key_id IN (...)` subquery based on scope:

| Role | Scope |
|---|---|
| `global_admin` / master_key | No filter (all logs) |
| `team_admin` | Keys owned by all users in their team |
| `member` | Keys owned by themselves only |

**Rationale**: Isolating scope in `ListLogsParams` keeps the Store interface
clean — no role logic leaks into the data layer; the web handler sets the correct
scope before calling `ListRequestLogs`.

---

## 7. Budget Fields (Schema-Only)

**Decision**: Add nullable `budget_limit_usd REAL` and `budget_period TEXT`
columns to `oidc_users`, `teams`, and `proxy_keys`. Add a `global_config` table
with a single row for org-level budget (`budget_limit_usd`, `budget_period`).

**Rationale**: Budget enforcement is deferred, but the schema must be in place
(FR-025, SC-008). Nullable columns with no application logic are zero-cost and
don't affect existing queries.

**Budget period values** (future enforcement): `'monthly'`, `'rolling_30d'`, `'lifetime'`.

---

## 8. Cookie Security Attributes

**Decision**: Session cookie attributes:
- `Name`: `llmp_session`
- `HttpOnly`: true (not accessible via JavaScript)
- `Secure`: true (HTTPS only; operator is responsible for TLS termination)
- `SameSite`: `Lax` (allows GET redirects from OIDC callback; blocks cross-site POST)
- `Path`: `/ui`
- `MaxAge`: 8 hours in seconds (28800)

**SameSite=Lax rationale**: The OIDC callback is a GET redirect from the IDP,
which `Strict` would block. `Lax` is the correct choice for OIDC flows.

---

## Summary: New Dependencies to Add

```
go get github.com/coreos/go-oidc/v3@latest
go get golang.org/x/oauth2@latest
```

Both are maintained by established Go ecosystem teams, have zero CGO
dependencies, and add minimal transitive dependency surface.
