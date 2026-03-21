package store

import (
	"context"
	"time"
)

// UserAdminStore contains the user-management operations used by the admin UI.
type UserAdminStore interface {
	ListUsersWithTeams(ctx context.Context) ([]UserWithTeam, error)
	GetOIDCUserByID(ctx context.Context, id string) (*OIDCUser, error)
	GetTeamMembership(ctx context.Context, userID string) (*TeamMembership, error)
	CountGlobalAdmins(ctx context.Context) (int64, error)
	UpdateOIDCUserRole(ctx context.Context, id, role string) error
	SetOIDCUserActive(ctx context.Context, id string, active bool) error
	DeleteUISessionsByUserID(ctx context.Context, userID string) error
	RecordAuditEvent(ctx context.Context, action, keyPrefix, detail, actorEmail string) error
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
	RecordAuditEvent(ctx context.Context, action, keyPrefix, detail, actorEmail string) error
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
	GetActivityStats(ctx context.Context, since time.Time) (*ActivityStats, error)
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
	GetModelUsageSummaryByUpstream(ctx context.Context, providerName, upstreamModel string) (*ModelUsageSummary, error)
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
	TrimRequestLogBodies(ctx context.Context, before time.Time, limit int) (int64, error)
	CountTrimmableLogBodies(ctx context.Context, before time.Time) (int64, error)
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
	RecordAuditEvent(ctx context.Context, action, keyPrefix, detail, actorEmail string) error
}

// AuditStore contains query operations for the audit event log.
type AuditStore interface {
	RecordAuditEvent(ctx context.Context, action, keyPrefix, detail, actorEmail string) error
	ListAuditEvents(ctx context.Context, params ListAuditParams) ([]AuditEvent, int64, error)
	ListDistinctAuditActions(ctx context.Context) ([]string, error)
}

// ListAuditParams controls filtering and pagination for audit event queries.
type ListAuditParams struct {
	Action string
	From   string // UTC datetime
	To     string // UTC datetime
	Page   int
	Limit  int
}

// KeyScopingStore contains per-key model allowlists and rate limit overrides.
type KeyScopingStore interface {
	GetKeyAllowedModels(ctx context.Context, keyID string) ([]string, error)
	SetKeyAllowedModels(ctx context.Context, keyID string, patterns []string) error
	GetKeyRateLimit(ctx context.Context, keyID string) (perMinute, burst int, ok bool, err error)
	SetKeyRateLimit(ctx context.Context, keyID string, perMinute, burst int) error
	ClearKeyRateLimit(ctx context.Context, keyID string) error
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
	AuditStore
	KeyScopingStore

	Ping(ctx context.Context) error
	Migrate(ctx context.Context) error
	Close() error
}
