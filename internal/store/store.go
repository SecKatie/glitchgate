// Package store provides the data-access layer for glitchgate.
package store

import (
	"context"
	"embed"
	"time"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store defines all data-access operations required by the proxy.
type Store interface {
	// Proxy keys.
	CreateProxyKey(ctx context.Context, id, keyHash, keyPrefix, label string) error
	GetActiveProxyKeyByPrefix(ctx context.Context, prefix string) (*ProxyKey, error)
	ListActiveProxyKeys(ctx context.Context) ([]ProxyKeySummary, error)
	RevokeProxyKey(ctx context.Context, prefix string) error
	UpdateKeyLabel(ctx context.Context, prefix, label string) error

	// Scoped proxy key queries.
	ListProxyKeysByOwner(ctx context.Context, ownerUserID string) ([]ProxyKeySummary, error)
	ListProxyKeysByTeam(ctx context.Context, teamID string) ([]ProxyKeySummary, error)
	CreateProxyKeyForUser(ctx context.Context, id, keyHash, keyPrefix, label, ownerUserID string) error

	// Audit and request logs.
	RecordAuditEvent(ctx context.Context, action, keyPrefix, detail string) error
	InsertRequestLog(ctx context.Context, log *RequestLogEntry) error
	ListRequestLogs(ctx context.Context, params ListLogsParams) ([]RequestLogSummary, int64, error)
	GetRequestLog(ctx context.Context, id string) (*RequestLogDetail, error)
	PruneRequestLogs(ctx context.Context, before time.Time, limit int) (int64, error)
	// ListDistinctModels returns all distinct model_requested values from
	// request_logs, ordered alphabetically.
	ListDistinctModels(ctx context.Context) ([]string, error)
	// ListDistinctStatuses returns all distinct response_status values from
	// request_logs, ordered numerically.
	ListDistinctStatuses(ctx context.Context) ([]int, error)
	// CountLogsSince returns the number of request log entries created after the
	// entry with the given ID that also match the active filter in params.
	// Returns 0 if sinceID is empty or not found.
	CountLogsSince(ctx context.Context, sinceID string, params ListLogsParams) (int64, error)

	// Cost queries.
	GetCostSummary(ctx context.Context, params CostParams) (*CostSummary, error)
	GetCostBreakdown(ctx context.Context, params CostParams) ([]CostBreakdownEntry, error)
	GetCostPricingGroups(ctx context.Context, params CostParams) ([]CostPricingGroup, error)
	GetCostTimeseries(ctx context.Context, params CostParams) ([]CostTimeseriesEntry, error)
	GetCostTimeseriesPricingGroups(ctx context.Context, params CostParams) ([]CostTimeseriesPricingGroup, error)

	// OIDC users.
	UpsertOIDCUser(ctx context.Context, subject, email, displayName string) (*OIDCUser, error)
	GetOIDCUserByID(ctx context.Context, id string) (*OIDCUser, error)
	GetOIDCUserBySubject(ctx context.Context, subject string) (*OIDCUser, error)
	ListOIDCUsers(ctx context.Context) ([]OIDCUser, error)
	CountGlobalAdmins(ctx context.Context) (int64, error)
	UpdateOIDCUserRole(ctx context.Context, id, role string) error
	SetOIDCUserActive(ctx context.Context, id string, active bool) error
	UpdateOIDCUserLastSeen(ctx context.Context, id string) error

	// Teams.
	CreateTeam(ctx context.Context, id, name, description string) error
	ListTeams(ctx context.Context) ([]Team, error)
	GetTeamByID(ctx context.Context, id string) (*Team, error)
	DeleteTeam(ctx context.Context, teamID string) error

	// Team memberships.
	AssignUserToTeam(ctx context.Context, userID, teamID string) error
	RemoveUserFromTeam(ctx context.Context, userID string) error
	GetTeamMembership(ctx context.Context, userID string) (*TeamMembership, error)
	ListTeamMembers(ctx context.Context, teamID string) ([]OIDCUser, error)

	// UI sessions.
	CreateUISession(ctx context.Context, id, token, sessionType, userID string, expiresAt time.Time) error
	GetUISessionByToken(ctx context.Context, token string) (*UISession, error)
	DeleteUISession(ctx context.Context, token string) error
	DeleteUISessionsByUserID(ctx context.Context, userID string) error
	CleanupExpiredSessions(ctx context.Context) error

	// OIDC state (PKCE).
	CreateOIDCState(ctx context.Context, state, pkceVerifier, redirectTo string, expiresAt time.Time) error
	ConsumeOIDCState(ctx context.Context, state string) (*OIDCState, error)
	CleanupExpiredOIDCState(ctx context.Context) error

	// Model usage queries.
	GetModelUsageSummary(ctx context.Context, modelName string) (*ModelUsageSummary, error)
	GetAllModelUsageSummaries(ctx context.Context) (map[string]*ModelUsageSummary, error)
	GetModelCostPricingGroups(ctx context.Context, modelName string) ([]CostPricingGroup, error)

	Migrate(ctx context.Context) error
	Close() error
}

// ModelUsageSummary holds aggregated usage statistics for a single model name.
type ModelUsageSummary struct {
	RequestCount             int64
	InputTokens              int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	OutputTokens             int64
	TotalCostUSD             float64
	ProviderName             string // most-recently-seen configured provider name from logs (may be empty)
	UpstreamModel            string // most-recently-seen model_upstream from logs (may be empty)
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
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
}

// CostTimeseriesEntry holds cost data for a single time bucket.
type CostTimeseriesEntry struct {
	Date     string
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

// AuditEvent records an administrative action for the audit trail.
type AuditEvent struct {
	ID        int64
	Action    string
	KeyPrefix string
	Detail    string
	CreatedAt time.Time
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

// Team represents an organizational group.
type Team struct {
	ID          string
	Name        string
	Description string
	CreatedAt   time.Time
	Budget      BudgetPolicy
}

// BudgetPolicy holds an optional spend limit for an entity.
type BudgetPolicy struct {
	LimitUSD *float64
	Period   *string
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
