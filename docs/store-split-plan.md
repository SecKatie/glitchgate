# Store Split Plan

## Goal

Reduce `store.Store` coupling without forcing a repo-wide rewrite. Phase 1 keeps the split conservative and targets the highest-leverage problems first: handler-side N+1 queries and overbroad request-path dependencies. Phase 2 expands the split once Phase 1 is stable.

## Phase 1: Conservative Request-Path Split

### Scope

- Add a small set of consumer-focused interfaces in `internal/store/store.go`.
- Add joined projection queries for user and team admin pages.
- Retype the narrowest consumers first.
- Keep `store.Store` in place as a compatibility umbrella during migration.

### Checklist

- [x] Add `UserAdminStore` to `internal/store/store.go`.
- [x] Add `TeamAdminStore` to `internal/store/store.go`.
- [x] Add `SessionReaderStore` to `internal/store/store.go`.
- [x] Add `SessionBackendStore` to `internal/store/store.go`.
- [x] Add `ProxyKeyAuthStore` to `internal/store/store.go`.
- [x] Add `RequestLogWriter` to `internal/store/store.go`.
- [x] Keep `Store` as a temporary compatibility interface.
- [x] Add a `UserWithTeam` projection type to the store layer.
- [x] Implement `ListUsersWithTeams(ctx)` in the SQLite store.
- [x] Add a `TeamWithMemberCount` projection type to the store layer.
- [x] Implement `ListTeamsWithMemberCounts(ctx)` in the SQLite store.
- [x] Retype `internal/web/user_handlers.go` to use `UserAdminStore`.
- [x] Replace handler-side user/team enrichment with `ListUsersWithTeams`.
- [x] Retype `internal/web/team_handlers.go` to use `TeamAdminStore`.
- [x] Replace handler-side team/member counting with `ListTeamsWithMemberCounts`.
- [x] Retype `internal/auth/session.go` to use `SessionBackendStore`.
- [x] Retype `internal/web/middleware.go` to use `SessionReaderStore`.
- [x] Retype `internal/proxy/middleware.go` to use `ProxyKeyAuthStore`.
- [x] Retype `internal/proxy/logger.go` to use `RequestLogWriter`.
- [x] Update constructor call sites to pass the narrower interfaces.
- [x] Update affected tests to implement the smaller interfaces directly.
- [x] Run targeted Go tests for the touched packages.
- [x] Run a broader verification pass and capture any unrelated pre-existing failures.

### Phase 1 Non-Goals

- [x] Do not remove `store.Store` yet.
- [x] Do not split broad web handlers like `internal/web/handlers.go` yet.
- [x] Do not change bootstrap/runtime ownership in `internal/app`.
- [x] Do not refactor SQLite internals beyond the new projections and interface conformance.

### Phase 1 Verification Notes

- Targeted verification passed: `go test ./internal/store ./internal/web ./internal/auth ./internal/proxy ./internal/app ./cmd/...`
- Broader verification still hits a pre-existing unrelated failure in `internal/translate`:
  `TestResponsesToAnthropic_UnsupportedToolType` in `internal/translate/responses_to_anthropic_test.go`

## Phase 2: Expanded Domain Split

### Scope

Expand from consumer-focused interfaces to domain-focused store boundaries once Phase 1 has reduced risk and test churn.

### Checklist

- [ ] Replace remaining `store.Store` request-path dependencies package by package.
- [ ] Split the broad web handlers into narrower log/key/model-oriented store dependencies.
- [ ] Introduce domain interfaces such as `KeyStore`, `LogStore`, `CostStore`, `UserStore`, `TeamStore`, `SessionStore`, and `OIDCStateStore` if they still add clarity after Phase 1.
- [ ] Decide whether `Store` should remain as an internal compatibility aggregate or be deleted entirely.
- [ ] Revisit startup/runtime concerns and decide whether maintenance/migration interfaces should be separated from request-path stores.
- [ ] Add any remaining joined projections needed by admin or reporting pages.
- [ ] Simplify test stubs across `web`, `proxy`, and `auth` so they no longer embed the umbrella store.
- [ ] Update review docs after the remaining consumers have moved off the umbrella store.

### Exit Criteria

- [ ] No request-path handler or middleware depends on `store.Store` unless it truly spans multiple domains.
- [ ] Admin pages no longer perform handler-side N+1 enrichment work.
- [ ] Test doubles are smaller and reflect the real consumer seams.
- [ ] The runtime/bootstrap layer uses only the store surface it actually needs.
