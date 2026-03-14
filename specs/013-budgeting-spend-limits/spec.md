# Feature Specification: Spend Budgeting with Daily, Weekly, and Monthly Limits

**Feature Branch**: `013-budgeting-spend-limits`
**Created**: 2026-03-13
**Status**: Draft
**Input**: User description: "build a budgeting feature that allows me to limit spend per day, week, and month"

## Existing Infrastructure

The database schema already includes budget tables created in migration 016:
- `user_budgets`, `team_budgets`, `proxy_key_budgets` (each with `limit_usd` and `period` columns, keyed to their parent entity)
- `global_budget_settings` table
- A `BudgetPolicy` struct (`LimitUSD *float64`, `Period *string`) in the store layer, embedded in `OIDCUser` and `Team`

The cost query infrastructure (`GetCostSummary`, `GetCostBreakdown`, `GetCostTimeseries`) already aggregates token spend from `request_logs` with date-range, user/team/key/provider scoping, and timezone-aware bucketing.

This feature builds enforcement, configuration UI, and visibility on top of these existing primitives.

## User Scenarios & Testing

### User Story 1 - Set and Enforce Spend Limits (Priority: P1)

As an administrator, I want to configure daily, weekly, and monthly spend limits so that I can control costs and prevent unexpected overages on my LLM API proxy.

**Why this priority**: Cost control is the primary motivation for users of glitchgate. Without spend limits, users risk runaway costs from high-volume usage or misconfigured clients.

**Independent Test**: Can be fully tested by configuring a daily limit of $10 and verifying that requests are rejected once cumulative spend for the day reaches $10.

**Acceptance Scenarios**:

1. **Given** no spend limits are configured, **When** an admin sets a daily limit of $50, **Then** the system accepts the configuration and begins tracking daily spend against this limit.

2. **Given** spend limits are configured, **When** an admin updates the weekly limit from $200 to $300, **Then** the new limit takes effect immediately and existing spend counts toward the updated limit.

3. **Given** a request is received and the cumulative spend for the current period already equals or exceeds the configured limit, **Then** the request is rejected before it is sent upstream, with an error message indicating the budget exceeded and when it resets.

4. **Given** a request is in progress (already sent upstream), **When** the cumulative spend for the period exceeds the limit due to another concurrent request completing, **Then** the in-progress request completes normally. Only subsequent new requests are rejected.

---

### User Story 2 - View Budget Status (Priority: P2)

As an administrator, I want to see current spend relative to my configured limits so that I can monitor usage and make informed decisions about budget adjustments.

**Why this priority**: Visibility into spend vs. limits is essential for proactive budget management and helps users understand their consumption patterns.

**Independent Test**: Can be fully tested by viewing the cost dashboard that shows $45 spent of $50 daily limit (90% utilized), $180 of $200 weekly limit, and $500 of $1000 monthly limit.

**Acceptance Scenarios**:

1. **Given** spend limits are configured and usage has occurred, **When** an admin views the cost dashboard, **Then** they see current spend and remaining budget for each configured period (day/week/month) alongside the existing cost breakdown.

2. **Given** spend is approaching a limit (e.g., 80% of daily limit), **When** the admin views the dashboard, **Then** they see a visual indicator (e.g., warning color) highlighting the approaching limit.

3. **Given** a limit has been exceeded, **When** the admin views the dashboard, **Then** they see that the limit is exceeded and can see when the budget will reset (next day/week/month).

---

### User Story 3 - Granular Budget Control (Priority: P3)

As an administrator, I want to apply different spend limits to different users, teams, or API keys so that I can allocate budgets based on entity needs and prevent any single user or team from consuming the entire organization budget.

**Why this priority**: Multi-tenant environments need budget isolation to ensure fair resource allocation and cost accountability across users and teams.

**Independent Test**: Can be fully tested by configuring a $20/day limit on a specific user and a $100/day global limit, then verifying the user is cut off at $20 while others continue up to $100.

**Acceptance Scenarios**:

1. **Given** user A has a $20 daily limit and the global daily limit is $100, **When** user A's spend reaches $20, **Then** user A's requests are rejected while other users' requests continue to be accepted.

2. **Given** a per-team budget of $50/day and a global daily budget of $200, **When** either the team's limit or the global limit is exceeded, **Then** the appropriate requests are rejected (enforcing the most restrictive applicable limit).

3. **Given** a user has a personal budget and belongs to a team with a team budget, **When** either the user's personal limit or the team's limit is exceeded, **Then** the user's requests are rejected.

---

### Edge Cases

- **Timezone boundaries**: Budget periods align to a configurable timezone (default UTC). Daily resets at midnight, weekly resets on a configurable day (default Monday), monthly resets on the 1st.

- **Multiple applicable limits**: When a request is subject to more than one budget (e.g., user + team + global), all applicable limits are checked and the most restrictive one governs. The rejection message identifies which specific limit was exceeded.

- **Limits lowered below current spend**: If a limit is lowered below the current period's spend, new requests are rejected immediately until the next period reset. In-progress requests complete normally.

- **Concurrent requests at budget boundary**: Budget enforcement is best-effort under concurrency. Multiple simultaneous requests may each pass the pre-flight check before any of them complete and update the spend counter. This means actual spend for a period may exceed the limit by up to N-1 requests' cost, where N is the number of concurrent in-flight requests. This is acceptable — the alternative (serialized budget checks) would add unacceptable latency to the proxy path.

- **Models without pricing data**: If a request uses a model with no configured pricing, that request's cost is treated as $0 for budget purposes. It will not count toward any spend limit. This prevents budget enforcement from blocking requests that the system cannot price.

- **Streaming responses**: Budget enforcement is a pre-flight check only — spend is assessed before the request is sent upstream. Once a request is accepted and streaming begins, it completes regardless of concurrent spend changes. Actual cost is recorded at completion and counts toward future budget checks.

## Requirements

### Functional Requirements

- **FR-001**: System MUST allow administrators to configure spend limits for daily, weekly, and monthly periods independently.

- **FR-002**: System MUST track cumulative spend for each configured period separately, derived from completed request costs in `request_logs`.

- **FR-003**: System MUST perform a pre-flight budget check before sending a request upstream. If cumulative spend for the current period already equals or exceeds the configured limit, the request is rejected without contacting the upstream provider.

- **FR-004**: System MUST reset spend tracking automatically at the start of each new period (daily at midnight, weekly on a configurable day, monthly on the 1st), all in the configured timezone.

- **FR-005**: System MUST display budget status (current spend, limit, remaining, percent used, next reset time) on the existing cost dashboard.

- **FR-006**: System MUST allow limits to be updated at any time, with changes taking effect on the next request.

- **FR-007**: System MUST support budgets at the following scopes, each tracked independently: global, per-user, per-team, and per-API-key. Each budget applies to exactly one scope dimension (no composite scoping like "user X on provider Y").

- **FR-008**: System MUST apply all applicable budgets to an incoming request and enforce the most restrictive one. For example, a request from user A using key K is checked against: the global budget, user A's budget (if any), user A's team budget (if any), and key K's budget (if any).

- **FR-009**: System MUST return a clear error response when a request is rejected due to budget limits, including: which limit was exceeded (scope and period), the current spend, the limit amount, and when the period resets.

- **FR-010**: System MUST treat requests to models with no configured pricing as $0 cost for budget purposes — they pass budget checks and do not accumulate spend.

### Budget Scope Model

Each budget is defined by exactly one scope dimension:

| Scope | Entity | Applies to |
|-------|--------|------------|
| Global | (singleton) | All requests |
| User | OIDC user ID | Requests authenticated by that user's proxy keys |
| Team | Team ID | Requests from any proxy key owned by a team member |
| API Key | Proxy key ID | Requests using that specific proxy key |

A single request may be subject to multiple budgets (e.g., global + user + team + key). All applicable budgets are checked; the first one exceeded causes rejection.

Per-provider and per-model budgets are excluded from this scope model. Provider-level cost control is better served by the existing provider subscription comparison on the cost dashboard. Model-level control is better served by not configuring expensive models in `model_list`.

### Key Entities

- **BudgetPolicy**: A configured limit for a specific scope and period. Stored in the existing `user_budgets`, `team_budgets`, `proxy_key_budgets`, and `global_budget_settings` tables. Each policy has a `limit_usd` and a `period` (daily, weekly, or monthly).

- **BudgetCheck**: A pre-flight evaluation performed before each proxied request. Queries cumulative spend for all applicable scopes and periods, compares against configured limits, and returns pass/fail with the failing limit details if any.

## Success Criteria

### Measurable Outcomes

- **SC-001**: Users can configure spend limits for daily, weekly, and monthly periods through the web UI in under 2 minutes.

- **SC-002**: The pre-flight budget check adds no more than 50ms to request latency (single SQLite query against indexed `request_logs`).

- **SC-003**: Under single-threaded request flow, no request is accepted when the budget for its period is already exhausted. Under concurrent load, actual spend may exceed the limit by at most the cost of concurrently in-flight requests at the moment the limit was reached.

- **SC-004**: Budget status on the cost dashboard reflects spend within 5 seconds of request completion (driven by the existing async log writer flush interval).

- **SC-005**: Budget period resets occur automatically without manual intervention or service restart.

- **SC-006**: Users can view budget status for all configured scopes and periods from the existing cost dashboard.

## Assumptions

- Spend is calculated using the same pricing logic already implemented in glitchgate (`internal/pricing`), based on input/output/cache/reasoning tokens and per-model rates.

- Budget enforcement is a pre-flight check against historical spend in `request_logs`. There is no separate running counter — spend is always derived from the source-of-truth log data. This avoids counter drift but means enforcement cost scales with query performance (mitigated by existing indexes).

- A request that fails the budget check is rejected entirely before contacting the upstream provider (no partial processing, no token consumption).

- Budget limits are soft at the concurrency boundary — if a request passes the pre-flight check and is already in flight when the limit is exceeded by another completing request, the in-flight request completes normally.

- Daily periods reset at midnight, weekly periods reset on a configurable day (default Monday) at midnight, monthly periods reset on the 1st at midnight — all in the configured display timezone (existing `TzLocation` from the cost dashboard).
