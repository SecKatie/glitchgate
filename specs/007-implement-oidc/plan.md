# Implementation Plan: OIDC Authentication for User & Team Management

**Branch**: `007-implement-oidc` | **Date**: 2026-03-11 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/007-implement-oidc/spec.md`

---

## Summary

Add OIDC-based authentication (authorization code flow + PKCE) to the web UI,
alongside the existing master key login (retained as a break-glass mechanism
hidden behind `?master=1`). Introduce a three-role user model (Global Admin,
Team Admin, Member), server-side SQLite-backed sessions, user-owned proxy API keys,
role-scoped log/key visibility, and team management. The data schema also includes
nullable budget limit fields at org/team/user/key scope to support a future
budget enforcement system.

**OIDC library**: `github.com/coreos/go-oidc/v3` + `golang.org/x/oauth2`
**Session storage**: SQLite `ui_sessions` table (replaces in-memory `auth.SessionStore`)
**Key ownership**: `proxy_keys.owner_user_id` → `oidc_users.id` (nullable; legacy keys unowned)

---

## Technical Context

**Language/Version**: Go 1.26.1 (module: `codeberg.org/kglitchy/llm-proxy`)
**Primary Dependencies**:
- Existing: `chi/v5`, `cobra`+`viper`, `modernc.org/sqlite`, `goose/v3`, `testify/require`, `golang.org/x/crypto`
- New (to add): `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2`

**Storage**: SQLite via `modernc.org/sqlite` — 8 new migrations (006–013)
**Testing**: `go test -race ./...` with `testify/require`; table-driven tests
**Target Platform**: Linux server, single static binary (`CGO_ENABLED=0`)
**Project Type**: Web service (HTTP proxy + embedded web UI)
**Performance Goals**: Session lookup ≤ 1ms (single indexed SQLite read); OIDC flow is off the hot path
**Constraints**: Binary stays single statically-linked; no CGO; `gosec` + `govulncheck` zero findings
**Scale/Scope**: ≥ 50 concurrent authenticated UI sessions (SC-005); single OIDC provider per deployment

---

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|---|---|---|
| I. Speed Above All | ✅ PASS | OIDC + session logic is entirely off the hot proxy path. Session lookup = 1 indexed SQLite read. No allocations added to `/v1/*` routes. |
| II. Efficient Resources | ✅ PASS | SQLite sessions replace the unbounded in-memory map, improving memory bounds. New dependencies (`go-oidc/v3`, `x/oauth2`) are well-contained, CGO-free. |
| III. Clean Abstractions | ✅ PASS | New `internal/oidc/` package for OIDC client; OIDC logic does not touch provider or translate packages. Authorization middleware separate from proxy auth middleware. |
| IV. Correctness | ✅ PASS | Not a translation/SSE feature; no impact on provider contracts. |
| V. Security by Default | ✅ PASS | PKCE mandatory (S256); HttpOnly+Secure+SameSite=Lax cookie; CSRF via state parameter; no secrets logged; master key form hidden by default; `gosec` must pass. |

**Post-design re-check**: All principles still hold after Phase 1 design.

**Complexity Tracking**: No constitution violations. No exceptions required.

---

## Project Structure

### Documentation (this feature)

```text
specs/007-implement-oidc/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/
│   └── ui-routes.md     # Phase 1 output
└── tasks.md             # Phase 2 output (/speckit.tasks command)
```

### Source Code Changes (repository root)

```text
internal/
├── auth/
│   ├── keys.go               # unchanged
│   ├── session.go            # REPLACE in-memory store → DB-backed (UISessionStore)
│   └── context.go            # NEW: UISessionContext type + context helpers
│
├── oidc/                     # NEW package
│   ├── provider.go           # OIDCProvider: wraps go-oidc + x/oauth2, builds auth URL, exchanges code
│   └── state.go              # OIDC state/PKCE: generate state+verifier, store in DB, consume on callback
│
├── config/
│   └── config.go             # EXTEND: add OIDCConfig struct; OIDCEnabled() helper
│
├── store/
│   ├── store.go              # EXTEND: Store interface with OIDC/team/session/key-owner methods + new types
│   ├── sqlite.go             # EXTEND: implement all new Store methods
│   └── migrations/
│       ├── 006_create_oidc_users.sql
│       ├── 007_create_teams.sql
│       ├── 008_create_team_memberships.sql
│       ├── 009_create_ui_sessions.sql
│       ├── 010_create_oidc_state.sql
│       ├── 011_add_owner_to_proxy_keys.sql
│       └── 012_add_budget_fields.sql
│       └── 013_create_global_config.sql
│
├── web/
│   ├── auth_handlers.go      # NEW: OIDC start (/ui/auth/oidc), callback (/ui/auth/callback)
│   ├── user_handlers.go      # NEW: users list, role change, deactivate/reactivate pages + API
│   ├── team_handlers.go      # NEW: teams list, create, member add/remove pages + API
│   ├── middleware.go         # NEW (web-layer): UISessionMiddleware, RequireGlobalAdmin, RequireAdminOrTA
│   ├── handlers.go           # MODIFY: LoginPage respects ?master=1; LoginHandler creates DB session
│   ├── conv.go               # MODIFY: ListRequestLogs callers pass scope from session context
│   ├── cost_handlers.go      # MODIFY: apply scope from session context
│   └── embed.go              # MODIFY: add new templates (users.html, teams.html, partials)
│
└── proxy/
    └── middleware.go         # unchanged (proxy API key auth is fully separate)

cmd/
└── serve.go                  # EXTEND: wire OIDCProvider, new routes, updated session store

go.mod / go.sum               # add go-oidc/v3, x/oauth2
```

---

## Implementation Phases

### Phase A — Foundation: Migrations & Store

**Goal**: All new DB tables exist and the `Store` interface exposes the new methods.
Nothing else changes in this phase; existing functionality is unaffected.

**Files**:
1. Write migrations 006–013 (DDL only).
2. Extend `Store` interface in `internal/store/store.go` with new types + methods.
3. Implement all new methods in `internal/store/sqlite.go`.
4. Write table-driven unit tests for every new Store method.

**Key constraints**:
- Migration 006–013 must apply cleanly on an existing database that already has 001–005.
- `ListRequestLogs` gains `ScopeType`, `ScopeUserID` fields on `ListLogsParams`; existing callers pass `ScopeType: "all"` (no behaviour change yet).
- `CreateProxyKeyForUser` is a new method; existing `CreateProxyKey` remains unchanged for master key session use.

---

### Phase B — Session & Auth Refactor

**Goal**: Replace the in-memory `auth.SessionStore` with a unified DB-backed session store.
Both master key logins and OIDC logins produce a row in `ui_sessions` with the same
token-in-cookie contract. Master key sessions use `session_type = 'master_key'` with
`user_id = NULL`; OIDC sessions use `session_type = 'oidc'` with `user_id` set.
All session lifecycle operations (create, validate, revoke, expiry cleanup) go through
the same code path regardless of how the user authenticated.

**Files**:
1. `internal/auth/session.go` — rewrite to implement `UISessionStore` backed by `store.Store` (create, validate, delete, cleanup).
2. `internal/auth/context.go` — `UISessionContext` struct; `SessionFromContext`, `ContextWithSession` helpers.
3. `internal/web/middleware.go` — `UISessionMiddleware` (replaces `web.SessionMiddleware`): reads `llmp_session` cookie → DB lookup → loads `OIDCUser` if OIDC session → injects `UISessionContext` into context. Redirects to `/ui/login` if invalid/expired.
4. `internal/web/middleware.go` — `RequireGlobalAdmin`, `RequireAdminOrTeamAdmin` middleware gates.
5. `cmd/serve.go` — swap `auth.NewSessionStore` → new DB-backed constructor; swap `web.SessionMiddleware` → `web.UISessionMiddleware`; wire `RequireGlobalAdmin` onto admin routes.
6. `internal/web/handlers.go` — `LoginHandler` creates a DB `ui_sessions` row (type `master_key`) instead of calling the in-memory store.

**Key constraints**:
- Master key login must work identically before and after this phase (SC-002).
- Cookie name `llmp_session`, attributes: `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/ui`, `MaxAge=28800`.
- Session cleanup goroutine started in `cmd/serve.go` at server startup.

---

### Phase C — OIDC Provider Package & Config

**Goal**: The OIDC authorization code flow with PKCE is implemented and testable,
but not yet wired into the HTTP router.

**Files**:
1. `internal/config/config.go` — add `OIDCConfig` struct; `OIDCEnabled()` method; viper mapping for `oidc.*` keys.
2. `internal/oidc/provider.go` — `OIDCProvider` struct wrapping `oidc.Provider` + `oauth2.Config`; methods: `AuthURL(state, pkceChallenge) string`, `Exchange(ctx, code, pkceVerifier) (*Claims, error)`. `Claims` carries `Subject`, `Email`, `DisplayName`.
3. `internal/oidc/state.go` — `GenerateState() string`, `GeneratePKCEPair() (verifier, challenge string)` (pure, testable); DB interactions delegated to Store methods.

**Key constraints**:
- `OIDCProvider` must not depend on `net/http` request/response types — pure exchange logic only.
- State + PKCE verifier generation functions must be independently unit-testable.
- `OIDCProvider` is `nil` when `oidc:` block is absent from config; handlers check `provider != nil` before rendering the OIDC button.

---

### Phase D — OIDC Login Flow (HTTP Handlers)

**Goal**: Users can sign in via OIDC. First user becomes Global Admin. Returning users are matched by subject.

**Files**:
1. `internal/web/auth_handlers.go` — `OIDCStartHandler` (GET `/ui/auth/oidc`): generates state+PKCE, stores in `oidc_state`, redirects to IDP.
2. `internal/web/auth_handlers.go` — `OIDCCallbackHandler` (GET `/ui/auth/callback`): validates state, exchanges code, verifies ID token, upserts `oidc_users`, creates `ui_sessions`, sets cookie, redirects.
3. `internal/web/handlers.go` — `LoginPage` updated: renders OIDC button when provider configured + no `?master=1`; renders master key form when `?master=1` or OIDC not configured.
4. `cmd/serve.go` — register `/ui/auth/oidc` and `/ui/auth/callback` routes (public, no session required).

**Key constraints**:
- First OIDC user (CountGlobalAdmins == 0) → role `global_admin` (FR-005).
- Deactivated user attempting OIDC login → 403, no session created (FR-013).
- IDP error in callback (`?error=...`) → render error page, no session.
- `oidc_state` entries expire after 10 minutes; stale state → 400 error page.
- Navigation bar shows authenticated user's `display_name` + `email` for OIDC sessions; "Admin" for master key sessions (FR-007).

---

### Phase E — User Management UI

**Goal**: Global Admin can list all users, change roles, and deactivate/reactivate.
Team Admin can manage users within their own team.

**Files**:
1. `internal/web/user_handlers.go` — `UsersPage` (GET `/ui/users`), `UsersAPIHandler` (GET `/ui/api/users`), `ChangeRoleHandler` (POST `/ui/api/users/{id}/role`), `DeactivateUserHandler`, `ReactivateUserHandler`.
2. Templates: `users.html` (HTMX-driven table, same Pico CSS style as existing pages).
3. `cmd/serve.go` — register routes; apply `RequireGlobalAdmin` on most user management routes.

**Key constraints**:
- `CountGlobalAdmins` guard before any role change or deactivation (FR-012).
- Deactivation must call `DeleteUISessionsByUserID` to immediately invalidate sessions (SC-003, FR-013).
- Team Admin scope: `DeactivateUserHandler` checks that target user is in the TA's team.

---

### Phase F — Team Management UI

**Goal**: Global Admin can create teams and assign users. Team Admin can manage their own team membership.

**Files**:
1. `internal/web/team_handlers.go` — `TeamsPage` (GET `/ui/teams`), `TeamsAPIHandler`, `CreateTeamHandler`, `AddTeamMemberHandler`, `RemoveTeamMemberHandler`.
2. Templates: `teams.html`.
3. `cmd/serve.go` — register routes; `RequireAdminOrTeamAdmin` gate.

**Key constraints**:
- `ON DELETE RESTRICT` on `team_memberships.team_id` prevents deleting teams with members; handler must return a user-friendly 409.
- Team Admin can only add/remove users from their own team (FR-016 enforcement).
- Reassigning a user already on a team silently replaces membership (`INSERT OR REPLACE`).

---

### Phase G — Role-Scoped Log, Key, and Cost Views

**Goal**: All existing `/ui/api/logs`, `/ui/api/keys`, `/ui/api/costs` routes respect the caller's role + team scope.

**Files**:
1. `internal/web/conv.go` — extract `buildScopeParams(ctx UISessionContext) ListLogsParams` helper; apply in `LogsAPIHandler` and `LogDetailAPIHandler`.
2. `internal/web/cost_handlers.go` — apply same scope helper.
3. `internal/web/handlers.go` — `KeysAPIHandler`, `CreateKeyHandler`, `RevokeKeyHandler` updated for role scope.

**Scope logic** (enforced in web layer, passed as params to Store):

| Session type | ScopeType | ScopeUserID | ScopeTeamID |
|---|---|---|---|
| master_key | `"all"` | — | — |
| global_admin | `"all"` | — | — |
| team_admin | `"team"` | — | user's team ID |
| member | `"user"` | user's ID | — |

**Key constraints**:
- Members see only their own keys and logs (FR-017, FR-018).
- Members see team-level cost aggregate (sum over team) but NOT per-peer breakdown (FR-019).
- Team Admin sees all keys + logs for their team (FR-020).
- No role logic in the Store layer; scope is injected via `ListLogsParams`.

---

### Phase H — Audit Logging

**Goal**: All auth + authorization events are recorded in `audit_events` (FR-023, SC-006).

**Events to record** (action strings):
- `oidc.login` — successful OIDC sign-in (detail: user email)
- `oidc.login_failed` — IDP error or deactivated user
- `master_key.login` — successful master key login
- `master_key.login_failed` — wrong key
- `session.logout` — explicit sign-out
- `user.role_changed` — detail: `"{id} → {new_role}"`
- `user.deactivated` / `user.reactivated`
- `team.created` / `team.member_added` / `team.member_removed`
- `key.created_for_user` / `key.revoked`

**Files**: Calls to `store.RecordAuditEvent` added in the handlers written in Phases D–G.

---

## Dependency Additions

```bash
go get github.com/coreos/go-oidc/v3@latest
go get golang.org/x/oauth2@latest
```

Both are CGO-free, widely used, and introduce minimal transitive dependencies.
Run `govulncheck ./...` after adding.

---

## Testing Strategy

| Layer | Approach |
|---|---|
| Store methods | Table-driven unit tests; real SQLite in-memory DB (`file::memory:?cache=shared`) |
| OIDC state/PKCE helpers | Pure unit tests (no DB) |
| Auth middleware | `httptest.NewRecorder` with DB-backed session store; test valid/expired/missing sessions |
| OIDC handlers | Mock `OIDCProvider` interface; test state validation, first-user bootstrap, deactivated-user rejection |
| Role authorization | Table-driven tests: each role × each endpoint → expected status |
| Scope enforcement | Unit tests for `buildScopeParams`; integration test verifying Member can't see peer logs |
| Login page | Test `?master=1` shows form; standard load hides form when OIDC configured |

---

## Migration from Current State

| Current | After this feature |
|---|---|
| In-memory `auth.SessionStore` | DB-backed `UISessionStore` in `ui_sessions` table |
| Master key form always visible | Hidden behind `?master=1` when OIDC configured |
| All proxy keys unowned | New keys owned by OIDC user; legacy keys remain with `owner_user_id = NULL` |
| Single session type | `oidc` or `master_key` session type in DB |
| `web.SessionMiddleware` | `web.UISessionMiddleware` (DB-backed; role-aware) |

**Zero-downtime migration**: All new columns are nullable with defaults. On first
startup after migration, existing in-memory sessions (master key) are lost — users
log back in via master key (`?master=1`) or OIDC. After this, sessions survive
restarts because they are persisted in SQLite.
