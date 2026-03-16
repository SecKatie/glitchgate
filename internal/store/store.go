// Package store provides the data-access layer for glitchgate.
package store

import (
	"context"
	"embed"
	"time"
)

//go:embed migrations/*.sql
var migrations embed.FS

// UserAdminStore contains the user-management operations used by the admin UI.
type UserAdminStore interface {
	ListUsersWithTeams(ctx context.Context) ([]UserWithTeam, error)
	GetOIDCUserByID(ctx context.Context, id string) (*OIDCUser, error)
	GetTeamMembership(ctx context.Context, userID string) (*TeamMembership, error)
	CountGlobalAdmins(ctx context.Context) (int64, error)
	UpdateOIDCUserRole(ctx context.Context, id, role string) error
	SetOIDCUserActive(ctx context.Context, id string, active bool) error
	DeleteUISessionsByUserID(ctx context.Context, userID string) error
	RecordAuditEvent(ctx context.Context, action, keyPrefix, detail string) error
}

// TeamAdminStore contains the team-management operations used by the admin UI.
type TeamAdminStore interface {
	ListTeams(ctx context.Context) ([]Team, error)
	ListTeamMembers(ctx context.Context, teamID string) ([]OIDCUser, error)
	ListTeamsWithMemberCounts(ctx context.Context) ([]TeamWithMemberCount, error)
	ListOIDCUsers(ctx context.Context) ([]OIDCUser, error)
	GetTeamByID(ctx context.Context, id string) (*Team, error)
	CreateTeam(ctx context.Context, id, name, description string) error
	AssignUserToTeam(ctx context.Context, userID, teamID string) error
	DeleteTeam(ctx context.Context, teamID string) error
	GetTeamMembership(ctx context.Context, userID string) (*TeamMembership, error)
	RemoveUserFromTeam(ctx context.Context, userID string) error
	RecordAuditEvent(ctx context.Context, action, keyPrefix, detail string) error
}

// SessionReaderStore contains the user lookups needed while validating UI sessions.
type SessionReaderStore interface {
	GetOIDCUserByID(ctx context.Context, id string) (*OIDCUser, error)
	GetTeamMembership(ctx context.Context, userID string) (*TeamMembership, error)
}

// SessionBackendStore contains the persistence operations used by UISessionStore.
type SessionBackendStore interface {
	CreateUISession(ctx context.Context, id, token, sessionType, userID string, expiresAt time.Time) error
	GetUISessionByToken(ctx context.Context, token string) (*UISession, error)
	DeleteUISession(ctx context.Context, token string) error
	DeleteUISessionsByUserID(ctx context.Context, userID string) error
	CleanupExpiredSessions(ctx context.Context) error
}

// ProxyKeyAuthStore contains the proxy-key lookup needed by auth middleware.
type ProxyKeyAuthStore interface {
	GetActiveProxyKeyByPrefix(ctx context.Context, prefix string) (*ProxyKey, error)
}

// RequestLogWriter contains the single write operation needed by AsyncLogger.
type RequestLogWriter interface {
	InsertRequestLog(ctx context.Context, log *RequestLogEntry) error
}

// ProxyKeyStore contains full proxy key CRUD operations.
type ProxyKeyStore interface {
	CreateProxyKey(ctx context.Context, id, keyHash, keyPrefix, label string) error
	CreateProxyKeyForUser(ctx context.Context, id, keyHash, keyPrefix, label, ownerUserID string) error
	ListActiveProxyKeys(ctx context.Context) ([]ProxyKeySummary, error)
	ListProxyKeysByOwner(ctx context.Context, ownerUserID string) ([]ProxyKeySummary, error)
	ListProxyKeysByTeam(ctx context.Context, teamID string) ([]ProxyKeySummary, error)
	RevokeProxyKey(ctx context.Context, prefix string) error
	UpdateKeyLabel(ctx context.Context, prefix, label string) error
}

// RequestLogStore contains request log query operations.
type RequestLogStore interface {
	RequestLogWriter
	ListRequestLogs(ctx context.Context, params ListLogsParams) ([]RequestLogSummary, int64, error)
	GetRequestLog(ctx context.Context, id string) (*RequestLogDetail, error)
	ListDistinctModels(ctx context.Context) ([]string, error)
	ListDistinctStatuses(ctx context.Context) ([]int, error)
	CountLogsSince(ctx context.Context, sinceID string, params ListLogsParams) (int64, error)
}

// CostQueryStore contains cost and billing analytics queries.
type CostQueryStore interface {
	GetCostSummary(ctx context.Context, params CostParams) (*CostSummary, error)
	GetCostBreakdown(ctx context.Context, params CostParams) ([]CostBreakdownEntry, error)
	GetCostPricingGroups(ctx context.Context, params CostParams) ([]CostPricingGroup, error)
	GetCostTimeseriesPricingGroups(ctx context.Context, params CostParams) ([]CostTimeseriesPricingGroup, error)
	GetCostTimeseries(ctx context.Context, params CostParams) ([]CostTimeseriesEntry, error)
}

// ModelUsageStore contains per-model usage statistics queries.
type ModelUsageStore interface {
	GetModelUsageSummary(ctx context.Context, modelName string) (*ModelUsageSummary, error)
	GetAllModelUsageSummaries(ctx context.Context) (map[string]*ModelUsageSummary, error)
	GetModelCostPricingGroups(ctx context.Context, modelName string) ([]CostPricingGroup, error)
	GetModelLatencyTimeseries(ctx context.Context, modelName string) ([]ModelLatencyTimeseriesEntry, error)
}

// OIDCStateStore contains OIDC authentication state management (PKCE flow).
type OIDCStateStore interface {
	CreateOIDCState(ctx context.Context, state, pkceVerifier, redirectTo string, expiresAt time.Time) error
	ConsumeOIDCState(ctx context.Context, state string) (*OIDCState, error)
}

// OIDCUserStore contains OIDC user CRUD operations.
type OIDCUserStore interface {
	UpsertOIDCUser(ctx context.Context, subject, email, displayName string) (*OIDCUser, error)
	GetOIDCUserBySubject(ctx context.Context, subject string) (*OIDCUser, error)
	UpdateOIDCUserLastSeen(ctx context.Context, id string) error
}

// MaintenanceStore contains periodic cleanup operations for the maintenance loop.
type MaintenanceStore interface {
	CleanupExpiredSessions(ctx context.Context) error
	CleanupExpiredOIDCState(ctx context.Context) error
	PruneRequestLogs(ctx context.Context, before time.Time, limit int) (int64, error)
}

// BudgetCheckStore contains the operations needed for budget enforcement and display.
type BudgetCheckStore interface {
	GetApplicableBudgets(ctx context.Context, proxyKeyID string) ([]ApplicableBudget, error)
	GetSpendSince(ctx context.Context, scope, scopeID string, since time.Time) (float64, error)
	GetBudgetsForScope(ctx context.Context, scopeType, userID, teamID string) ([]ApplicableBudget, error)
}

// BudgetAdminStore contains administrative operations for managing budget limits.
type BudgetAdminStore interface {
	SetGlobalBudget(ctx context.Context, limitUSD float64, period string) error
	ClearGlobalBudget(ctx context.Context) error
	SetUserBudget(ctx context.Context, userID string, limitUSD float64, period string) error
	ClearUserBudget(ctx context.Context, userID string) error
	SetTeamBudget(ctx context.Context, teamID string, limitUSD float64, period string) error
	ClearTeamBudget(ctx context.Context, teamID string) error
	SetKeyBudget(ctx context.Context, keyID string, limitUSD float64, period string) error
	ClearKeyBudget(ctx context.Context, keyID string) error
	RecordAuditEvent(ctx context.Context, action, keyPrefix, detail string) error
}

// Store defines all data-access operations required by the proxy. It composes
// narrow interfaces so that consumers can depend on the smallest surface they
// need. Prefer accepting a narrow interface for new code.
type Store interface {
	UserAdminStore
	TeamAdminStore
	SessionReaderStore
	SessionBackendStore
	ProxyKeyAuthStore
	RequestLogWriter
	ProxyKeyStore
	RequestLogStore
	CostQueryStore
	ModelUsageStore
	OIDCStateStore
	OIDCUserStore
	MaintenanceStore
	BudgetCheckStore
	BudgetAdminStore

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
