# Research: Models Page

**Branch**: `011-models-page` | **Date**: 2026-03-13

## R-001: How does the existing web UI pass config to handlers?

**Decision**: Pass `[]config.ModelMapping` and `[]config.ProviderConfig` as fields on `Handlers`, supplied at construction in `serve.go`.

**Rationale**: `Handlers` currently receives `cfg.MasterKey` and `calc` (derived from config) but not the full config. The cleanest pattern — consistent with how the calculator is passed — is to supply only the slices needed (`ModelList`, `Providers`) rather than the full `*config.Config`. This avoids Handlers having unnecessary access to secrets like `APIKey` or `MasterKey`.

**Alternatives considered**:
- Pass full `*config.Config` — rejected: gives Handlers access to all secrets (APIKey, MasterKey) unnecessarily (Principle V).
- Re-read config from disk at request time — rejected: violates startup-loaded config contract; adds I/O on every page load.

---

## R-002: What store query is needed for per-model usage statistics?

**Decision**: Add a new `GetModelUsageSummary(ctx, modelName string) (*ModelUsageSummary, error)` method to the `Store` interface and `SQLiteStore`.

**Rationale**: The existing `GetCostBreakdown` query groups by `model_upstream`, but the Models page needs stats keyed by `model_requested` (the client-facing name from the config). A dedicated targeted query is simpler and more efficient than reusing the general cost breakdown.

**Query pattern**:
```sql
SELECT
    COUNT(*) AS request_count,
    COALESCE(SUM(input_tokens), 0) AS input_tokens,
    COALESCE(SUM(output_tokens), 0) AS output_tokens,
    COALESCE(SUM(estimated_cost_usd), 0) AS total_cost_usd
FROM request_logs
WHERE model_requested = ?
```

**Alternatives considered**:
- Reuse `GetCostBreakdown` with group-by model — rejected: returns all models at once; caller would have to filter. Also groups by `model_upstream`, not `model_requested`.
- Reuse `GetCostSummary` with a model filter — rejected: doesn't return per-model breakdown, just totals.

---

## R-003: How should model names with slashes be handled in URLs?

**Decision**: URL-encode the model name when building the detail route. The chi router path parameter is decoded automatically; the handler receives the raw model name.

**Rationale**: Model names like `gc/claude-sonnet-4-6` contain slashes. Using a catch-all route pattern (`/ui/models/{model:.+}`) in chi allows slashes in the path parameter. Alternatively, URL-encode the slash as `%2F` in the list page links — chi handles both correctly.

**Chosen approach**: Use `url.PathEscape(modelName)` in the template when building detail links. The chi route is registered as `/ui/models/{model}` with a wildcard suffix to allow slashes.

**Alternatives considered**:
- Base64-encode the model name — rejected: unnecessary complexity; URL encoding is standard.
- Use query param `?model=gc/...` instead of path param — viable fallback if path routing proves awkward, but path params are more RESTful and consistent with existing `/ui/logs/{id}` pattern.

---

## R-004: What should the example curl command look like?

**Decision**: Generate a `curl` command targeting the Anthropic Messages API endpoint (`POST /v1/messages`) with a placeholder API key and the model's exact `model_name` from config.

**Format**:
```bash
curl https://your-glitchgate-host/v1/messages \
  -H "x-api-key: YOUR_PROXY_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model_name>",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

**Rationale**: The Messages API is the primary format. A note below the example can indicate the OpenAI-compatible endpoint (`/v1/chat/completions`) is also supported.

**Alternatives considered**:
- Show both Anthropic and OpenAI curl examples — viable enhancement but adds template complexity; can be added later.
- Dynamically insert the actual listen address from config — rejected: address may be `0.0.0.0:4000` (not useful); using a placeholder hostname is clearer.

---

## R-005: How are wildcard models displayed?

**Decision**: Wildcard entries (e.g., `gc/*`) appear as a single row in the list with model name shown as-is. The detail page shows "Wildcard pattern" as the model type with the provider name. No usage stats aggregation makes sense for wildcards (clients use specific names like `gc/claude-sonnet`), so usage is shown as "N/A — usage tracked per resolved model name."

**Rationale**: Wildcard entries don't have a fixed `model_requested` value in logs — each request uses a specific name. Aggregating all `gc/*` hits would require a `LIKE` query, which is a future enhancement.

**Alternatives considered**:
- Hide wildcards from the Models page — rejected: operators need to know what wildcard patterns are configured.
- Aggregate usage with `LIKE 'gc/%'` — viable future enhancement; out of scope for this feature.

---

## R-006: Where does pricing data come from for the list page?

**Decision**: Use `pricing.Calculator.Lookup(providerName, upstreamModel)` for models with a direct provider. For virtual/fallback models, show pricing for the first concrete model in the fallback chain (if resolvable).

**Rationale**: The `Calculator` already holds the merged pricing table (defaults + config overrides). The `Lookup` method returns `(Entry, bool)` — the bool signals whether pricing is known.

**For the handler**: The handler receives `[]config.ModelMapping` and iterates. For each direct model, it calls `Lookup(provider.Type + "/" + mapping.UpstreamModel)` — wait, actually the calculator key is `providerName/upstreamModel` where `providerName` is the provider's `Name` field (not `Type`). Confirmed by reading `serve.go` and `calculator.go`.

**Confirmed key format**: `providerName + "/" + upstreamModel` where `providerName` is `ProviderConfig.Name` (e.g., `"anthropic"`, `"copilot"`), not `Type`.
