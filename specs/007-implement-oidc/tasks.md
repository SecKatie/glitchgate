# Tasks: OIDC Authentication for User & Team Management

**Input**: Design documents from `/specs/007-implement-oidc/`
**Prerequisites**: plan.md ✅, spec.md ✅, research.md ✅, data-model.md ✅, contracts/ui-routes.md ✅, quickstart.md ✅

**Organization**: Tasks grouped by user story to enable independent implementation and testing.

## Format: `[ID] [P?] [Story?] Description`

- **[P]**: Can run in parallel (different files, no dependencies on other in-progress tasks)
- **[Story]**: Which user story this task belongs to (US1–US4)

---

## Phase 1: Setup

**Purpose**: Add new dependencies; create empty package skeletons.

- [ ] T001 Add `github.com/coreos/go-oidc/v3` and `golang.org/x/oauth2` to `go.mod` and `go.sum` via `go get`; run `govulncheck ./...` to confirm zero new findings
- [ ] T002 [P] Create `internal/oidc/` package with stub `doc.go` (package comment: "Package oidc implements the OIDC authorization code flow client.")
- [ ] T003 [P] Create `internal/auth/context.go` stub (package declaration only; will be filled in Phase 2)

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: All new DB tables, Store interface extensions, unified session store, auth middleware framework, and config extension. No user story work can begin until this phase is complete.

**⚠️ CRITICAL**: All user story phases depend on this phase.

### Migrations

- [ ] T004 [P] Create `internal/store/migrations/006_create_oidc_users.sql` — `oidc_users` table with `id`, `subject` (UNIQUE), `email`, `display_name`, `role` CHECK constraint, `active`, `last_seen_at`, `created_at`, `budget_limit_usd`, `budget_period`; indexes on `subject`, `email`, `role`
- [ ] T005 [P] Create `internal/store/migrations/007_create_teams.sql` — `teams` table with `id`, `name` (UNIQUE), `description`, `created_at`, `budget_limit_usd`, `budget_period`
- [ ] T006 [P] Create `internal/store/migrations/008_create_team_memberships.sql` — `team_memberships` table with `user_id` (PK, FK → oidc_users CASCADE), `team_id` (FK → teams RESTRICT), `joined_at`; index on `team_id`
- [ ] T007 [P] Create `internal/store/migrations/009_create_ui_sessions.sql` — `ui_sessions` table with `id`, `token` (UNIQUE), `session_type` CHECK constraint, `user_id` (FK → oidc_users CASCADE, nullable), `created_at`, `expires_at`; indexes on `token`, `expires_at`, `user_id`
- [ ] T008 [P] Create `internal/store/migrations/010_create_oidc_state.sql` — `oidc_state` table with `state` (PK), `pkce_verifier`, `redirect_to`, `created_at`, `expires_at`; index on `expires_at`
- [ ] T009 [P] Create `internal/store/migrations/011_add_owner_to_proxy_keys.sql` — `ALTER TABLE proxy_keys ADD COLUMN owner_user_id TEXT REFERENCES oidc_users(id)`; index on `owner_user_id`
- [ ] T010 [P] Create `internal/store/migrations/012_add_budget_fields.sql` — `ALTER TABLE proxy_keys ADD COLUMN budget_limit_usd REAL; ADD COLUMN budget_period TEXT`
- [ ] T011 [P] Create `internal/store/migrations/013_create_global_config.sql` — `global_config` table with `key TEXT PRIMARY KEY, value TEXT NOT NULL`; seed rows for `budget_limit_usd` and `budget_period`

### Store — Types and Interface

- [ ] T012 Add `OIDCUser`, `Team`, `TeamMembership`, `UISession`, `OIDCState` types to `internal/store/store.go` (fields per `data-model.md`)
- [ ] T013 Add OIDC user methods to `Store` interface in `internal/store/store.go`: `UpsertOIDCUser`, `GetOIDCUserByID`, `GetOIDCUserBySubject`, `ListOIDCUsers`, `CountGlobalAdmins`, `UpdateOIDCUserRole`, `SetOIDCUserActive`, `UpdateOIDCUserLastSeen`
- [ ] T014 Add team and membership methods to `Store` interface in `internal/store/store.go`: `CreateTeam`, `ListTeams`, `GetTeamByID`, `AssignUserToTeam`, `RemoveUserFromTeam`, `GetTeamMembership`, `ListTeamMembers`
- [ ] T015 Add session and state methods to `Store` interface in `internal/store/store.go`: `CreateUISession`, `GetUISessionByToken`, `DeleteUISession`, `DeleteUISessionsByUserID`, `CleanupExpiredSessions`, `CreateOIDCState`, `ConsumeOIDCState`, `CleanupExpiredOIDCState`
- [ ] T016 Extend `ListLogsParams` struct in `internal/store/store.go` with `ScopeType string` (`"all"` | `"team"` | `"user"`), `ScopeUserID string`, `ScopeTeamID string`; update all existing callers of `ListRequestLogs` to pass `ScopeType: "all"` (no behaviour change yet)
- [ ] T017 Add scoped key methods to `Store` interface in `internal/store/store.go`: `ListProxyKeysByOwner`, `ListProxyKeysByTeam`, `CreateProxyKeyForUser`

### Store — SQLite Implementations

- [ ] T018 [P] Implement OIDC user methods in `internal/store/sqlite.go`: `UpsertOIDCUser` (INSERT OR REPLACE preserving role on update), `GetOIDCUserByID`, `GetOIDCUserBySubject`, `ListOIDCUsers`, `CountGlobalAdmins`, `UpdateOIDCUserRole`, `SetOIDCUserActive`, `UpdateOIDCUserLastSeen`; table-driven unit tests in `internal/store/oidc_users_test.go`
- [ ] T019 [P] Implement team and membership methods in `internal/store/sqlite.go`: `CreateTeam`, `ListTeams`, `GetTeamByID`, `AssignUserToTeam` (INSERT OR REPLACE), `RemoveUserFromTeam`, `GetTeamMembership`, `ListTeamMembers`; unit tests in `internal/store/teams_test.go`
- [ ] T020 [P] Implement UI session methods in `internal/store/sqlite.go`: `CreateUISession`, `GetUISessionByToken`, `DeleteUISession`, `DeleteUISessionsByUserID`, `CleanupExpiredSessions`; unit tests in `internal/store/ui_sessions_test.go`
- [ ] T021 [P] Implement OIDC state methods in `internal/store/sqlite.go`: `CreateOIDCState`, `ConsumeOIDCState` (SELECT + DELETE in a single transaction), `CleanupExpiredOIDCState`; unit tests in `internal/store/oidc_state_test.go`
- [ ] T022 [P] Implement scoped key and log methods in `internal/store/sqlite.go`: `ListProxyKeysByOwner`, `ListProxyKeysByTeam`, `CreateProxyKeyForUser`; update `ListRequestLogs` to apply `ScopeType`-based `WHERE proxy_key_id IN (...)` subquery; unit tests in `internal/store/scoped_queries_test.go`

### Session Refactor

- [ ] T023 Rewrite `internal/auth/session.go` as `UISessionStore` backed by `store.Store`: `Create(ctx, sessionType, userID)`, `Validate(ctx, token)`, `Delete(ctx, token)`, `DeleteAllForUser(ctx, userID)`, `Cleanup(ctx)`; remove the in-memory `map[string]*Session`
- [ ] T024 Implement `internal/auth/context.go`: `UISessionContext` struct (SessionID, SessionType, IsMasterKey, User `*store.OIDCUser`, TeamID `*string`, Role string); `SessionFromContext(ctx)`, `ContextWithSession(ctx, *UISessionContext)` helpers
- [ ] T025 Create `internal/web/middleware.go` with: `UISessionMiddleware` (reads `llmp_session` cookie → `GetUISessionByToken` → loads `OIDCUser` if OIDC session → injects `UISessionContext` → redirects to `/ui/login` if invalid); `RequireGlobalAdmin` (403 if not global_admin/master_key); `RequireAdminOrTeamAdmin` (403 if member)
- [ ] T026 Update `cmd/serve.go`: replace `auth.NewSessionStore` with `UISessionStore`; replace `web.SessionMiddleware` with `web.UISessionMiddleware`; apply `RequireGlobalAdmin` to admin-only routes; start background goroutine calling `CleanupExpiredSessions` and `CleanupExpiredOIDCState` every hour

### Config Extension

- [ ] T027 Add `OIDCConfig` struct (`IssuerURL`, `ClientID`, `ClientSecret`, `RedirectURL`, `Scopes []string`) and `OIDCConfig *OIDCConfig` field to `Config` in `internal/config/config.go`; add `OIDCEnabled() bool` method; add viper mappings for `oidc.*` keys; `Scopes` defaults to `[openid email profile]`

**Checkpoint**: Migrations, Store, session store, middleware framework, and config are all ready. Master key login continues to work (now using DB-backed sessions). OIDC-specific features can now be built in parallel per user story.

---

## Phase 3: User Story 1 — OIDC Sign-In Alongside Master Key (Priority: P1) 🎯 MVP

**Goal**: Any user with an IDP account can sign in via OIDC. Master key login still works, hidden behind `?master=1`.

**Independent Test**: Configure a test OIDC provider; verify: (1) standard `/ui/login` shows only OIDC button, (2) OIDC flow completes and user sees their name in the nav, (3) `/ui/login?master=1` shows master key form which still works, (4) OIDC-unconfigured instance shows only master key form, (5) IDP rejection shows error, no session.

### Implementation for US1

- [ ] T028 [P] [US1] Implement `internal/oidc/provider.go`: `OIDCProvider` struct; `NewOIDCProvider(cfg OIDCConfig) (*OIDCProvider, error)` (initializes `go-oidc` provider + `oauth2.Config`); `AuthURL(state, pkceChallenge string) string`; `Exchange(ctx, code, pkceVerifier string) (*Claims, error)` (token exchange + ID token verification returning `Claims{Subject, Email, DisplayName}`)
- [ ] T029 [P] [US1] Implement `internal/oidc/state.go`: `GenerateState() (string, error)` (32-byte random hex); `GeneratePKCEPair() (verifier, challenge string, err error)` (uses `oauth2.GenerateVerifier()` + `oauth2.S256ChallengeOption`); pure functions with no DB dependency; unit tests in `internal/oidc/state_test.go`
- [ ] T030 [US1] Update `LoginPage` handler in `internal/web/handlers.go`: pass `oidcEnabled bool` and `showMasterKeyForm bool` (true when `?master=1` present or OIDC not configured) to the login template; redirect to `/ui/` if a valid session already exists
- [ ] T031 [US1] Update login template (`internal/web/` templates) to conditionally render the OIDC "Sign In" button and/or master key form per `oidcEnabled` + `showMasterKeyForm` flags; add logged-in user name/email (or "Admin") to nav bar partial
- [ ] T032 [US1] Create `OIDCStartHandler` (GET `/ui/auth/oidc`) in `internal/web/auth_handlers.go`: call `GenerateState` + `GeneratePKCEPair`; store in `oidc_state` via `CreateOIDCState` (10 min TTL); redirect to `provider.AuthURL(state, challenge)`; return 404 if OIDC not configured
- [ ] T033 [US1] Create `OIDCCallbackHandler` (GET `/ui/auth/callback`) in `internal/web/auth_handlers.go`: validate `?error` param (render error page if set); consume `oidc_state` row by state param (400 if missing/expired); call `provider.Exchange` with PKCE verifier; upsert `oidc_users` via `UpsertOIDCUser`; assign role (`global_admin` if `CountGlobalAdmins == 0`, else `member`); create `ui_sessions` row (type `oidc`); set `llmp_session` cookie (HttpOnly, Secure, SameSite=Lax, Path=/ui, MaxAge=28800); redirect to `redirect_to` or `/ui/`
- [ ] T034 [US1] Register `/ui/auth/oidc` and `/ui/auth/callback` routes (public, no session required) in `cmd/serve.go`; wire `OIDCProvider` (nil when OIDC not configured); pass provider to `web.NewHandlers` or auth handler constructors

**Checkpoint**: Full OIDC sign-in works end-to-end. Master key still works via `?master=1`. Named session established. Nav shows user identity.

---

## Phase 4: User Story 2 — First OIDC User Becomes Global Admin (Priority: P2)

**Goal**: First sign-in auto-assigns Global Admin role. Subsequent sign-ins get Member. Returning users preserve their role. Master key sessions are always Global Admin-equivalent.

**Independent Test**: (1) Sign in with no existing users → verify Global Admin role in Users section; (2) sign in with a second account → verify Member role; (3) sign in again with first account → role unchanged; (4) use master key session → verify access to all data pages.

**Dependency**: Requires Phase 3 complete (`OIDCCallbackHandler` is already wired; this phase adds correctness details on top).

### Implementation for US2

- [ ] T035 [US2] Audit `UpsertOIDCUser` implementation in `internal/store/sqlite.go`: ensure the SQL does NOT overwrite `role` or `active` on conflict (only update `email`, `display_name`, `last_seen_at`); add explicit test case for returning user role preservation in `internal/store/oidc_users_test.go`
- [ ] T036 [US2] Add deactivated-user guard in `OIDCCallbackHandler` (`internal/web/auth_handlers.go`): after upsert, fetch user; if `active == false`, return 403 error page and do not create a session
- [ ] T037 [US2] Update `UISessionMiddleware` in `internal/web/middleware.go`: for OIDC sessions, fetch the user on each request and check `active`; if deactivated, call `DeleteUISession` and redirect to `/ui/login?error=deactivated`; for master_key sessions, set `Role: "global_admin"` and `IsMasterKey: true` in `UISessionContext` without any user table lookup

**Checkpoint**: Role bootstrap works. Deactivated users lose access immediately. Master key sessions have unconditional Global Admin context.

---

## Phase 5: User Story 3 — Admin Manages OIDC Users and Roles (Priority: P3)

**Goal**: Global Admin sees all users, can change roles, deactivate/reactivate. Team Admin manages users in their own team.

**Independent Test**: (1) Sign in as Global Admin → Users page lists all users with role + last seen; (2) promote a Member to Team Admin → role takes effect on next request; (3) demote → role revoked; (4) deactivate → user's next request is rejected; (5) attempt to deactivate last Global Admin → 409 error.

**Dependency**: Requires Phase 3 + Phase 4.

### Implementation for US3

- [ ] T038 [P] [US3] Create `UsersPage` (GET `/ui/users`) and `UsersAPIHandler` (GET `/ui/api/users`) in `internal/web/user_handlers.go`: GA returns all users; returns JSON `{users: [...]}` with id, email, display_name, role, active, team_id, team_name, last_seen_at, created_at
- [ ] T039 [P] [US3] Create users management HTML template in `internal/web/` templates: HTMX-driven table matching existing Pico CSS style; role badge, active/inactive indicator, action buttons (change role, deactivate/reactivate)
- [ ] T040 [US3] Create `ChangeRoleHandler` (POST `/ui/api/users/{id}/role`) in `internal/web/user_handlers.go`: validate role value; call `CountGlobalAdmins` guard before demoting any `global_admin` (409 if last); call `UpdateOIDCUserRole`; record audit event `user.role_changed`
- [ ] T041 [US3] Create `DeactivateUserHandler` (POST `/ui/api/users/{id}/deactivate`) in `internal/web/user_handlers.go`: GA can deactivate any user; TA can only deactivate users in their own team (403 otherwise); `CountGlobalAdmins` guard (409 if last GA); call `SetOIDCUserActive(false)` then `DeleteUISessionsByUserID`; record audit event `user.deactivated`
- [ ] T042 [US3] Create `ReactivateUserHandler` (POST `/ui/api/users/{id}/reactivate`) in `internal/web/user_handlers.go`: GA only; call `SetOIDCUserActive(true)`; record audit event `user.reactivated`
- [ ] T043 [US3] Register `/ui/users`, `/ui/api/users`, `/ui/api/users/{id}/role`, `/ui/api/users/{id}/deactivate`, `/ui/api/users/{id}/reactivate` routes in `cmd/serve.go`; apply `RequireGlobalAdmin` to all except deactivate (which uses `RequireAdminOrTeamAdmin`)

**Checkpoint**: User management UI functional. Role changes take effect immediately. Deactivation invalidates sessions within one request (SC-003).

---

## Phase 6: User Story 4 — Team Management, Key Ownership, and Scoped Views (Priority: P4)

**Goal**: Global Admin creates teams and assigns users. Each user owns their own keys. Members see only their own logs/keys plus team cost aggregate. Team Admins see all team logs/keys. Admins see everything.

**Independent Test**: (1) Create team, assign two users → each member sees only their own logs; (2) Team Admin sees all team logs; (3) GA sees everything; (4) member sees team cost aggregate; (5) unassigned member sees only own data with empty-team notice.

**Dependency**: Requires Phase 5.

### Implementation for US4

- [ ] T044 [P] [US4] Create `TeamsPage` (GET `/ui/teams`) and `TeamsAPIHandler` (GET `/ui/api/teams`) in `internal/web/team_handlers.go`: GA returns all teams with member count; TA returns only their own team
- [ ] T045 [P] [US4] Create teams management HTML template in `internal/web/` templates: HTMX-driven; create team form (GA only); member list per team; add/remove member controls
- [ ] T046 [US4] Create `CreateTeamHandler` (POST `/ui/api/teams`) in `internal/web/team_handlers.go`: GA only; generate UUID; call `CreateTeam`; 409 if name already exists; record audit event `team.created`
- [ ] T047 [US4] Create `AddTeamMemberHandler` (POST `/ui/api/teams/{id}/members`) in `internal/web/team_handlers.go`: GA can add any unassigned user; TA can only add to their own team (403 otherwise); call `AssignUserToTeam` (replaces existing membership); record audit event `team.member_added`
- [ ] T048 [US4] Create `RemoveTeamMemberHandler` (DELETE `/ui/api/teams/{id}/members/{userID}`) in `internal/web/team_handlers.go`: GA or TA (own team only); call `RemoveUserFromTeam`; record audit event `team.member_removed`
- [ ] T049 [US4] Register all team routes in `cmd/serve.go`; apply `RequireAdminOrTeamAdmin` to teams routes; apply `RequireGlobalAdmin` to `CreateTeamHandler`
- [ ] T050 [US4] Update `CreateKeyHandler` in `internal/web/handlers.go`: for OIDC sessions call `CreateProxyKeyForUser` with `owner_user_id`; for master key sessions call existing `CreateProxyKey` (unowned); update `RevokeKeyHandler` to enforce scope (GA: any key; TA: team members' keys; Member: own keys only; 403 otherwise)
- [ ] T051 [US4] Update `KeysAPIHandler` in `internal/web/handlers.go`: call `ListProxyKeysByOwner` for member sessions, `ListProxyKeysByTeam` for team admin sessions, existing `ListActiveProxyKeys` for GA/master key sessions
- [ ] T052 [US4] Add `buildScopeParams` helper in `internal/web/` (e.g. `internal/web/scope.go`): takes `UISessionContext` and returns populated `ScopeType`, `ScopeUserID`, `ScopeTeamID` fields for `ListLogsParams`
- [ ] T053 [US4] Apply scope in `LogsAPIHandler` and `LogDetailAPIHandler` in `internal/web/conv.go`: call `buildScopeParams`; pass to `ListRequestLogs`; Members requesting a log detail for a log they don't own get 403
- [ ] T054 [US4] Apply scope in cost handlers in `internal/web/cost_handlers.go`: GA/master key: full aggregate; Team Admin: team-scoped aggregate; Member: own key aggregate + team total (two queries); unassigned member: own key aggregate only

**Checkpoint**: Full team management, key ownership, and role-scoped data views working (US4 complete, SC-004 satisfied).

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: Audit logging across all auth/admin operations, error UX, final hardening.

- [ ] T055 Add audit event recording (`store.RecordAuditEvent`) for all authentication events in `internal/web/auth_handlers.go` and `internal/web/handlers.go`: `oidc.login`, `oidc.login_failed` (IDP error + deactivated user), `master_key.login`, `master_key.login_failed`, `session.logout` (FR-023, SC-006)
- [ ] T056 Add audit event recording for all admin operations across `internal/web/user_handlers.go`, `internal/web/team_handlers.go`, `internal/web/handlers.go`: `user.role_changed`, `user.deactivated`, `user.reactivated`, `team.created`, `team.member_added`, `team.member_removed`, `key.created_for_user`, `key.revoked`
- [ ] T057 [P] Add user-facing error pages / HTMX error fragments for: OIDC callback errors, deactivated account (403), last-admin guard (409), IDP unavailable — consistent with existing Pico CSS style
- [ ] T058 [P] Update `docs/configuration.md` and `README.md` to document the `oidc:` config block, `?master=1` break-glass usage, and role model; cross-reference `specs/007-implement-oidc/quickstart.md`
- [X] T059 Run `make lint` (`golangci-lint`), `make audit` (`gosec` + `govulncheck`), and `go test -race ./...` — resolve all findings before merging
- [ ] T060 Validate end-to-end against `specs/007-implement-oidc/quickstart.md`: configure a test OIDC provider; walk through bootstrap, team creation, member scoping, and break-glass recovery; confirm SC-001 through SC-008

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (Setup)**: No dependencies — start immediately
- **Phase 2 (Foundational)**: Depends on Phase 1 — **BLOCKS all user story phases**
- **Phase 3 (US1)**: Depends on Phase 2
- **Phase 4 (US2)**: Depends on Phase 3 (builds on callback handler)
- **Phase 5 (US3)**: Depends on Phase 4 (requires users + roles to exist)
- **Phase 6 (US4)**: Depends on Phase 5 (requires user management to work)
- **Phase 7 (Polish)**: Depends on Phase 6

### User Story Dependencies

```
Phase 1 (Setup)
    └── Phase 2 (Foundational: migrations, store, sessions, middleware, config)
            └── Phase 3 (US1: OIDC sign-in flow)
                    └── Phase 4 (US2: first-user bootstrap correctness)
                            └── Phase 5 (US3: user + role management)
                                    └── Phase 6 (US4: teams + scoped views)
                                            └── Phase 7 (Polish + audit)
```

**Note**: US1 through US4 are sequential in this feature because each story builds on the previous (you need users before you can manage them; you need users in teams before you can scope views).

### Within Each Phase

- Tasks marked **[P]** within a phase can run in parallel (operate on different files)
- Phase 2 migrations (T004–T011): all parallel — separate SQL files
- Phase 2 store implementations (T018–T022): all parallel — different method groups
- Phase 2 session refactor (T023–T026): sequential — each depends on the prior type/interface
- Phase 3 T028+T029: parallel — `provider.go` and `state.go` are independent files
- Phase 5 T038+T039: parallel — handler and template are independent
- Phase 6 T044+T045: parallel — handler and template are independent

### Parallel Opportunities

```bash
# Phase 2 — run all migrations in parallel:
T004  T005  T006  T007  T008  T009  T010  T011

# Phase 2 — run store implementations in parallel (after T012–T017):
T018  T019  T020  T021  T022

# Phase 3 — run OIDC package files in parallel:
T028  T029
```

---

## Implementation Strategy

### MVP Scope (Phase 1 + Phase 2 + Phase 3)

Complete Phases 1–3 to deliver a working OIDC sign-in alongside the existing master key. This is independently deployable and already satisfies the core feature motivation.

1. Phase 1: Add dependencies, create skeletons
2. Phase 2: Foundation (migrations, store, session refactor, config) — **longest phase**
3. Phase 3: OIDC sign-in, login page update, callback handler, route registration
4. **STOP and VALIDATE**: test OIDC sign-in + master key coexistence
5. Deploy / demo if ready

### Incremental Delivery

1. MVP (Phases 1–3): OIDC sign-in works, master key break-glass works
2. Add Phase 4: First-user bootstrap correctness + deactivation guard
3. Add Phase 5: User management UI — delegation possible
4. Add Phase 6: Teams + scoped views — full organizational model
5. Add Phase 7: Audit logging + hardening + docs

---

## Notes

- `[P]` tasks operate on separate files and have no in-progress dependencies
- All new Go code must pass `golangci-lint`, `gosec`, `govulncheck`, and `go test -race ./...`
- Commit after each logical task group; keep commits atomic
- The proxy API routes (`/v1/*`) are completely unaffected — `proxy.AuthMiddleware` is separate from all UI session logic
- Legacy proxy keys (`owner_user_id = NULL`) remain fully functional throughout
- Budget fields are schema-only in this feature — no enforcement logic should be written
