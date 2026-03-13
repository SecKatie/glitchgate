# Glitchgate Code Quality Review

*2026-03-13T16:33:02Z by Showboat 0.6.1*
<!-- showboat-id: aedfdb6b-3ddb-46c7-90ff-6351bc83fd6a -->

This review focuses on duplication, abstraction drift, and broken concepts that increase the maintenance cost of glitchgate. I prioritized findings by refactor value and risk, not by file size. The most urgent issue is a likely authorization bug in key scoping; the most leveraged refactor is to unify proxy request orchestration and application bootstrap.

```bash
rg --files internal cmd | xargs wc -l | sort -n | tail -n 12
```

```output
     545 internal/proxy/openai_handler_test.go
     559 internal/proxy/responses_handler.go
     567 internal/proxy/responses_handler_test.go
     588 internal/web/cost_handlers.go
     602 internal/proxy/handler.go
     615 internal/translate/responses_stream_translator.go
     837 internal/web/handlers.go
     966 internal/translate/translate_test.go
    1174 internal/store/sqlite.go
    1247 internal/proxy/handler_test.go
    1460 internal/web/conv.go
   28696 total
```

```bash
rg -n "ListProxyKeysByOwner\(r\.Context\(\), sc\.SessionID\)|return \"user\", sc\.User\.ID" internal/web/handlers.go internal/web/scope.go | sort
```

```output
internal/web/handlers.go:642:		return h.store.ListProxyKeysByOwner(r.Context(), sc.SessionID)
internal/web/scope.go:30:			return "user", sc.User.ID, ""
internal/web/scope.go:35:			return "user", sc.User.ID, ""
```

```bash
rg -n "CostUSD: +0|TotalCostUSD: +0" internal/web/cost_handlers.go
```

```output
204:			CostUSD:             0,
224:		TotalCostUSD:             0,
269:			CostUSD:  0,
```

```bash
rg -n "if prov.APIFormat\(\) == \"openai\"|if prov.APIFormat\(\) == \"responses\"" internal/proxy/handler.go internal/proxy/openai_handler.go | sort
```

```output
internal/proxy/handler.go:113:		if prov.APIFormat() == "openai" {
internal/proxy/handler.go:119:		if prov.APIFormat() == "responses" {
internal/proxy/openai_handler.go:90:		if prov.APIFormat() == "openai" {
internal/proxy/openai_handler.go:96:		if prov.APIFormat() == "responses" {
```

```bash
rg -n "CreateProxyKey\(|GetActiveProxyKeyByPrefix\(|GetCostSummary\(|UpsertOIDCUser\(|CreateUISession\(|CreateOIDCState\(|GetModelUsageSummary\(|Migrate\(" internal/store/store.go
```

```output
16:	CreateProxyKey(ctx context.Context, id, keyHash, keyPrefix, label string) error
17:	GetActiveProxyKeyByPrefix(ctx context.Context, prefix string) (*ProxyKey, error)
45:	GetCostSummary(ctx context.Context, params CostParams) (*CostSummary, error)
51:	UpsertOIDCUser(ctx context.Context, subject, email, displayName string) (*OIDCUser, error)
73:	CreateUISession(ctx context.Context, id, token, sessionType, userID string, expiresAt time.Time) error
80:	CreateOIDCState(ctx context.Context, state, pkceVerifier, redirectTo string, expiresAt time.Time) error
85:	GetModelUsageSummary(ctx context.Context, modelName string) (*ModelUsageSummary, error)
89:	Migrate(ctx context.Context) error
```

```bash
rg -n "GetTeamMembership\(|GetTeamByID\(|ListTeamMembers\(|InputPerMillion|CacheWritePerMillion|CacheReadPerMillion|OutputPerMillion" internal/web/user_handlers.go internal/web/team_handlers.go internal/web/model_handlers.go internal/web/costview.go | sort
```

```output
internal/web/costview.go:100:	outputCost := float64(log.OutputTokens) * entry.OutputPerMillion / 1_000_000
internal/web/costview.go:112:	cb.InputRatePerMillion = entry.InputPerMillion
internal/web/costview.go:113:	cb.CacheWriteRatePerMillion = entry.CacheWritePerMillion
internal/web/costview.go:114:	cb.CacheReadRatePerMillion = entry.CacheReadPerMillion
internal/web/costview.go:115:	cb.OutputRatePerMillion = entry.OutputPerMillion
internal/web/costview.go:134:		cost := float64(group.InputTokens)*entry.InputPerMillion/1_000_000 +
internal/web/costview.go:135:			float64(group.CacheCreationTokens)*entry.CacheWritePerMillion/1_000_000 +
internal/web/costview.go:136:			float64(group.CacheReadTokens)*entry.CacheReadPerMillion/1_000_000 +
internal/web/costview.go:137:			float64(group.OutputTokens)*entry.OutputPerMillion/1_000_000
internal/web/costview.go:183:		cb.InputCostUSD += float64(group.InputTokens) * entry.InputPerMillion / 1_000_000
internal/web/costview.go:184:		cb.CacheWriteCostUSD += float64(group.CacheCreationTokens) * entry.CacheWritePerMillion / 1_000_000
internal/web/costview.go:185:		cb.CacheReadCostUSD += float64(group.CacheReadTokens) * entry.CacheReadPerMillion / 1_000_000
internal/web/costview.go:186:		cb.OutputCostUSD += float64(group.OutputTokens) * entry.OutputPerMillion / 1_000_000
internal/web/costview.go:64:				cost := float64(log.InputTokens)*entry.InputPerMillion/1_000_000 +
internal/web/costview.go:65:					float64(log.CacheCreationInputTokens)*entry.CacheWritePerMillion/1_000_000 +
internal/web/costview.go:66:					float64(log.CacheReadInputTokens)*entry.CacheReadPerMillion/1_000_000 +
internal/web/costview.go:67:					float64(log.OutputTokens)*entry.OutputPerMillion/1_000_000
internal/web/costview.go:96:	inputCost := float64(log.InputTokens) * entry.InputPerMillion / 1_000_000
internal/web/costview.go:97:	cacheWriteCost := float64(log.CacheCreationInputTokens) * entry.CacheWritePerMillion / 1_000_000
internal/web/costview.go:98:	cacheReadCost := float64(log.CacheReadInputTokens) * entry.CacheReadPerMillion / 1_000_000
internal/web/costview.go:99:	reasoningCost := float64(log.ReasoningTokens) * entry.OutputPerMillion / 1_000_000
internal/web/model_handlers.go:142:			items[i].TotalSpendUSD = float64(u.InputTokens)*p.InputPerMillion/1_000_000 +
internal/web/model_handlers.go:143:				float64(u.CacheCreationInputTokens)*p.CacheWritePerMillion/1_000_000 +
internal/web/model_handlers.go:144:				float64(u.CacheReadInputTokens)*p.CacheReadPerMillion/1_000_000 +
internal/web/model_handlers.go:145:				float64(u.OutputTokens)*p.OutputPerMillion/1_000_000
internal/web/model_handlers.go:215:		usage.TotalCostUSD = float64(usage.InputTokens)*p.InputPerMillion/1_000_000 +
internal/web/model_handlers.go:216:			float64(usage.CacheCreationInputTokens)*p.CacheWritePerMillion/1_000_000 +
internal/web/model_handlers.go:217:			float64(usage.CacheReadInputTokens)*p.CacheReadPerMillion/1_000_000 +
internal/web/model_handlers.go:218:			float64(usage.OutputTokens)*p.OutputPerMillion/1_000_000
internal/web/team_handlers.go:110:		members, err := h.store.ListTeamMembers(r.Context(), t.ID)
internal/web/team_handlers.go:199:	team, err := h.store.GetTeamByID(r.Context(), teamID)
internal/web/team_handlers.go:236:	tm, err := h.store.GetTeamMembership(r.Context(), userID)
internal/web/team_handlers.go:86:		team, err := h.store.GetTeamByID(r.Context(), *sc.TeamID)
internal/web/team_handlers.go:90:		members, err := h.store.ListTeamMembers(r.Context(), team.ID)
internal/web/user_handlers.go:153:		tm, err := h.store.GetTeamMembership(r.Context(), id)
internal/web/user_handlers.go:92:		if tm, err := h.store.GetTeamMembership(r.Context(), u.ID); err == nil && tm != nil {
internal/web/user_handlers.go:94:			if team, err := h.store.GetTeamByID(r.Context(), tm.TeamID); err == nil {
```

## High-value refactors

1. Unify proxy request orchestration and fallback semantics.

The three proxy entrypoints repeat the same lifecycle: parse request, resolve model chain, branch on `APIFormat()`, send upstream, retry on fallback statuses, log, then translate the response. The duplication is in `internal/proxy/handler.go:58`, `internal/proxy/openai_handler.go:38`, and `internal/proxy/responses_handler.go:38`. It already caused behavioral skew before the 2026-03-13 fix: `internal/proxy/handler.go:113-121` and `internal/proxy/openai_handler.go:90-98` returned into format-specific helpers before the outer fallback loop could continue, so cross-format fallback was effectively bypassed in those paths.

Refactor target: introduce a single proxy pipeline that works in terms of source format, provider capabilities, request translators, and response adapters. Make retry/fallback policy live in one place. This is the highest leverage change because every new model-routing feature, provider type, and logging rule currently has to be implemented three times.

Status 2026-03-13: the cross-format fallback continuation bug is fixed in the Anthropic and OpenAI entrypoints. The broader proxy-pipeline unification is still open.

2. Extract application bootstrap and provider compilation out of `cmd/serve.go`.

`runServe` is doing full application composition rather than acting as a thin CLI entrypoint. In one function it loads config, opens the DB, runs migrations, normalizes runtime settings, builds providers, seeds pricing, wires routes, and starts background jobs (`cmd/serve.go:46-180` and later). Provider identity is also recomputed in multiple loops for client creation, pricing keys, metadata overrides, and UI naming.

Refactor target: add an `internal/app` or `internal/bootstrap` package that returns a composed runtime object, and add a `ProviderRegistry` that resolves unique configured provider names, default base URLs, pricing defaults, and instantiated clients once. Provider identity should stay keyed to `ProviderConfig.Name`; type and host detection should only select default pricing tables or one-time legacy backfills. This removes the current "change one provider concept in four places" problem.

Status 2026-03-13: provider identity and runtime cost attribution now use `ProviderConfig.Name`. Legacy canonical host/type keys are only retained for historical log normalization and default-rate selection.

3. Centralize authorization policy and fix the current scope bug first.

The most concrete correctness issue I found is in key visibility. `internal/web/handlers.go:638-642` falls back to `ListProxyKeysByOwner(..., sc.SessionID)` for a team admin without a team, while `internal/web/scope.go:24-31` treats the same situation as user-scoped via `sc.User.ID`. That mixes session identity with user identity in authorization code. More broadly, policy is duplicated across log detail, key revoke, user management, and team management paths instead of being expressed as reusable `CanViewLog`, `CanManageKey`, `CanManageUser`, and `VisibleKeyScope` helpers.

Refactor target: fix the `sc.SessionID` bug immediately, then move authorization decisions into a small policy module used by handlers. This should reduce drift and make scope behavior testable as policy rather than incidental control flow.

Status 2026-03-13: the `sc.SessionID` bug is fixed and key visibility now flows through a shared scope helper. The broader authorization-policy extraction is still open.

4. Make cost calculation a shared service and make cost APIs truthful.

The web package repeats token-to-cost math in `internal/web/model_handlers.go:142-145`, `internal/web/model_handlers.go:215-218`, and throughout `internal/web/costview.go:57-186`. At the same time, the JSON cost endpoints return zero-dollar placeholders even though the HTML path computes real pricing later: `internal/web/cost_handlers.go:200-224` and `internal/web/cost_handlers.go:265-270` hardcode `CostUSD` and `TotalCostUSD` to `0`.

Refactor target: move all view-facing pricing computation behind one service or presenter layer that consumes `pricing.Calculator`. Use it for JSON APIs, HTML pages, log detail, and model detail. This is important before tiered pricing lands, because the current scattering will make that feature expensive and error-prone.

Status 2026-03-13: completed. Cost math now goes through shared helpers, JSON summary/timeseries endpoints return computed dollars, and the web layer uses pricing-aware grouping for truthful totals.

5. Split the store contract by domain and move list projections into the store layer.

`internal/store/store.go:13-90` combines proxy keys, logs, costs, OIDC users, teams, sessions, OIDC state, model usage, migrations, and close semantics into one god interface. The UI then compensates with N+1 enrichment in handlers, for example `internal/web/user_handlers.go:72-97` and `internal/web/team_handlers.go:103-118`.

Refactor target: split `Store` into narrower interfaces such as `KeyStore`, `LogStore`, `CostStore`, `UserStore`, and `SessionStore`, and add store-level projection queries for user lists with team info and team lists with member counts. That reduces fake complexity in tests, removes handler chatiness, and gives the storage layer room to optimize queries.

6. Consolidate SSE and provider transport infrastructure.

Streaming logic is duplicated across `internal/proxy/stream.go`, `internal/translate/stream_translator.go`, `internal/translate/reverse_stream.go`, and `internal/translate/responses_reverse_stream.go`. Provider clients also reimplement near-identical HTTP execution pipelines in `internal/provider/anthropic/client.go`, `internal/provider/openai/client.go`, and `internal/provider/copilot/client.go`. There are already correctness issues in the translation layer, including `input_json_delta` being a documented no-op in `internal/translate/stream_translator.go:141-144`.

Refactor target: build one shared SSE engine with pluggable event translators, and one shared provider executor with provider-specific auth/header/token hooks. This reduces the chance that a stream fix or transport hardening change lands in only one path.

## Concrete bugs worth fixing first

- Fixed 2026-03-13: `internal/web/handlers.go:642` should almost certainly use `sc.User.ID`, not `sc.SessionID`.
- Fixed 2026-03-13: `internal/web/cost_handlers.go:204`, `internal/web/cost_handlers.go:224`, and `internal/web/cost_handlers.go:269` made the cost APIs lie about dollars.
- Fixed 2026-03-13: `internal/proxy/handler.go:113-121` and `internal/proxy/openai_handler.go:90-98` bypassed outer fallback continuation for cross-format providers.
- `internal/translate/stream_translator.go:141-144` drops streamed tool-call argument deltas.

## Suggested order of work

1. Completed 2026-03-13: Fix the scope bug and add focused authorization policy tests.
2. Completed 2026-03-13: repair cost API semantics by routing all cost views through one pricing presenter.
3. Completed 2026-03-13: repair cross-format fallback continuation in the Anthropic and OpenAI proxy entrypoints.
4. Next: collapse proxy orchestration into one fallback-aware pipeline.
5. Extract bootstrap/provider compilation from `cmd/serve.go`.
6. Split `store.Store` and add joined projection queries for admin pages.
7. Unify SSE and transport helpers.
