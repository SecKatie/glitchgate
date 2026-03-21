package store

import "time"

// ModelUsageSummary holds aggregated usage statistics for a single model name.
type ModelUsageSummary struct {
	RequestCount             int64
	InputTokens              int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	OutputTokens             int64
	TotalCostUSD             float64
	LogCostUSD               float64 // SUM of per-request cost_usd from logs (always available, even without pricing config)
	ProviderName             string  // most-recently-seen configured provider name from logs (may be empty)
	UpstreamModel            string  // most-recently-seen model_upstream from logs (may be empty)
}

// ProxyKey represents a full proxy-key row, including the hash.
type ProxyKey struct {
	ID        string
	KeyHash   string
	KeyPrefix string
	Label     string
	CreatedAt time.Time
	RevokedAt *time.Time
}

// ProxyKeySummary is a read-only projection of a proxy key without the hash.
type ProxyKeySummary struct {
	ID        string
	KeyPrefix string
	Label     string
	CreatedAt time.Time
}

// RequestLogEntry holds everything needed to persist a single proxied request.
type RequestLogEntry struct {
	ID                       string
	ProxyKeyID               string
	Timestamp                time.Time
	SourceFormat             string
	ProviderName             string
	ModelRequested           string
	ModelUpstream            string
	ResolvedModelName        string // actual model used after fallback resolution
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	ReasoningTokens          int64
	LatencyMs                int64
	Status                   int
	RequestBody              string
	ResponseBody             string
	ErrorDetails             *string
	IsStreaming              bool
	FallbackAttempts         int64
	CostUSD                  *float64
}

// ListLogsParams controls filtering, sorting, and pagination for log listing.
type ListLogsParams struct {
	Page        int
	PerPage     int
	Model       string
	Status      int
	KeyPrefix   string
	From        string
	To          string
	Sort        string
	Order       string
	ScopeType   string // "all" | "team" | "user"
	ScopeUserID string
	ScopeTeamID string
}

// RequestLogSummary is a read-only projection of a request log without bodies.
type RequestLogSummary struct {
	ID                       string
	Timestamp                time.Time
	SourceFormat             string
	ProviderName             string
	ModelRequested           string
	ModelUpstream            string
	ProxyKeyPrefix           string
	ProxyKeyLabel            string
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	ReasoningTokens          int64
	LatencyMs                int64
	Status                   int
	IsStreaming              bool
	ErrorDetails             *string
	FallbackAttempts         int64
}

// RequestLogDetail extends RequestLogSummary with full request/response bodies.
type RequestLogDetail struct {
	RequestLogSummary
	RequestBody  string
	ResponseBody string
}

// CostParams controls filtering for cost queries.
type CostParams struct {
	From        string // Start datetime (inclusive), UTC, e.g. "2026-03-01 00:00:00"
	To          string // End datetime (inclusive), UTC, e.g. "2026-03-11 23:59:59"
	GroupBy     string // "model", "key", or "provider"
	KeyPrefix   string // Optional filter by proxy key prefix when grouping by key
	GroupFilter string // Optional prefix filter on the current group dimension (model or provider)
	// ProviderGroups is an optional set of exact provider_name values to include
	// when GroupBy == "provider". These should normally be configured provider
	// names after startup normalization has rewritten legacy raw keys.
	ProviderGroups []string
	ScopeType      string // "all" | "team" | "user"
	ScopeUserID    string
	ScopeTeamID    string
	// TzOffsetSeconds is the UTC offset of the display timezone in seconds
	// (positive = east, e.g. +28800 for UTC+8, -18000 for UTC-5).
	// Used to bucket timestamps by local date in the timeseries query.
	TzOffsetSeconds int
	// TzLocation is the display timezone used to bucket timestamps by local
	// calendar day. When set, it takes precedence over TzOffsetSeconds so
	// DST transitions are handled per timestamp.
	TzLocation *time.Location
}

// CostSummary holds aggregated cost totals for a date range.
type CostSummary struct {
	TotalInputTokens         int64
	TotalOutputTokens        int64
	TotalCacheCreationTokens int64
	TotalCacheReadTokens     int64
	TotalRequests            int64
}

// ActivityStats holds aggregate request activity metrics for a time period.
type ActivityStats struct {
	TotalRequests int64
	ErrorCount    int64 // status >= 400
	AvgLatencyMs  float64
}

// CostBreakdownEntry holds cost aggregated by a grouping dimension.
type CostBreakdownEntry struct {
	Group               string
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	Requests            int64
}

// CostPricingGroup holds token totals grouped by exact provider/model pair so
// the web layer can apply pricing rates without ambiguity.
type CostPricingGroup struct {
	ModelRequested      string
	ProviderName        string
	ModelUpstream       string
	ProxyKeyPrefix      string // populated only when GroupBy == "key"
	ProxyKeyGroup       string // rendered key label, e.g. "llmp_sk_xxxx (my key)", populated only when GroupBy == "key"
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	ReasoningTokens     int64
	Requests            int64
}

// CostTimeseriesEntry holds cost data for a single time bucket.
type CostTimeseriesEntry struct {
	Date     string
	CostUSD  float64
	Requests int64
}

// CostTimeseriesPricingGroup holds token totals grouped by local date and exact
// provider/model pair so the web layer can compute truthful timeseries costs.
type CostTimeseriesPricingGroup struct {
	Date                string
	ProviderName        string
	ModelUpstream       string
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	Requests            int64
}

// ModelLatencyTimeseriesEntry holds hourly latency-per-output-token data for a model.
type ModelLatencyTimeseriesEntry struct {
	Bucket              string  // "YYYY-MM-DD HH" (hour-precision UTC)
	AvgMsPerOutputToken float64 // SUM(latency_ms) / SUM(output_tokens)
	TotalLatencyMs      int64
	TotalOutputTokens   int64
	Requests            int64
}

// AuditEvent records an administrative action for the audit trail.
type AuditEvent struct {
	ID         int64
	Action     string
	KeyPrefix  string
	Detail     string
	ActorEmail string // empty for pre-migration rows and system events
	CreatedAt  time.Time
}

// OIDCUser represents an OIDC-authenticated user account.
type OIDCUser struct {
	ID          string
	Subject     string
	Email       string
	DisplayName string
	Role        string // "global_admin" | "team_admin" | "member"
	Active      bool
	LastSeenAt  *time.Time
	CreatedAt   time.Time
	Budget      BudgetPolicy
}

// UserWithTeam is a store-level projection for the user admin page.
type UserWithTeam struct {
	ID          string
	Email       string
	DisplayName string
	Role        string
	Active      bool
	LastSeenAt  *time.Time
	CreatedAt   time.Time
	TeamID      *string
	TeamName    *string
}

// Team represents an organizational group.
type Team struct {
	ID          string
	Name        string
	Description string
	CreatedAt   time.Time
	Budget      BudgetPolicy
}

// TeamWithMemberCount is a store-level projection for the team admin page.
type TeamWithMemberCount struct {
	ID          string
	Name        string
	Description string
	CreatedAt   time.Time
	MemberCount int
}

// BudgetPolicy holds an optional spend limit for an entity.
type BudgetPolicy struct {
	LimitUSD *float64
	Period   *string
}

// ApplicableBudget represents a single budget that applies to a request.
type ApplicableBudget struct {
	Scope    string // "global", "user", "team", "key"
	ScopeID  string // entity ID ("" for global)
	LimitUSD float64
	Period   string // "daily", "weekly", "monthly"
}

// BudgetViolation holds details about a budget limit that was exceeded.
type BudgetViolation struct {
	Scope    string
	ScopeID  string
	Period   string
	LimitUSD float64
	SpendUSD float64
	ResetAt  time.Time
}

// TeamMembership records a user's assignment to a team.
type TeamMembership struct {
	UserID   string
	TeamID   string
	JoinedAt time.Time
}

// UISession represents a web UI session stored in the database.
type UISession struct {
	ID          string
	Token       string
	SessionType string // "oidc" | "master_key"
	UserID      *string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// OIDCState holds the short-lived state and PKCE verifier for the OIDC flow.
type OIDCState struct {
	State        string
	PKCEVerifier string
	RedirectTo   string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}
