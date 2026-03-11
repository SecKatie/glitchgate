// Package store provides the data-access layer for llm-proxy.
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
	CreateProxyKey(ctx context.Context, id, keyHash, keyPrefix, label string) error
	GetActiveProxyKeyByPrefix(ctx context.Context, prefix string) (*ProxyKey, error)
	ListActiveProxyKeys(ctx context.Context) ([]ProxyKeySummary, error)
	RevokeProxyKey(ctx context.Context, prefix string) error
	UpdateKeyLabel(ctx context.Context, prefix, label string) error
	RecordAuditEvent(ctx context.Context, action, keyPrefix, detail string) error
	InsertRequestLog(ctx context.Context, log *RequestLogEntry) error
	ListRequestLogs(ctx context.Context, params ListLogsParams) ([]RequestLogSummary, int64, error)
	GetRequestLog(ctx context.Context, id string) (*RequestLogDetail, error)
	GetCostSummary(ctx context.Context, params CostParams) (*CostSummary, error)
	GetCostBreakdown(ctx context.Context, params CostParams) ([]CostBreakdownEntry, error)
	GetCostTimeseries(ctx context.Context, params CostParams) ([]CostTimeseriesEntry, error)
	// ListDistinctModels returns all distinct model_requested values from
	// request_logs, ordered alphabetically.
	ListDistinctModels(ctx context.Context) ([]string, error)
	// CountLogsSince returns the number of request log entries created after the
	// entry with the given ID that also match the active filter in params.
	// Returns 0 if sinceID is empty or not found.
	CountLogsSince(ctx context.Context, sinceID string, params ListLogsParams) (int64, error)
	Migrate(ctx context.Context) error
	Close() error
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
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	LatencyMs                int64
	Status                   int
	RequestBody              string
	ResponseBody             string
	EstimatedCostUSD         *float64
	ErrorDetails             *string
	IsStreaming              bool
}

// ListLogsParams controls filtering, sorting, and pagination for log listing.
type ListLogsParams struct {
	Page      int
	PerPage   int
	Model     string
	Status    int
	KeyPrefix string
	From      string
	To        string
	Sort      string
	Order     string
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
	LatencyMs                int64
	Status                   int
	EstimatedCostUSD         *float64
	IsStreaming              bool
	ErrorDetails             *string
}

// RequestLogDetail extends RequestLogSummary with full request/response bodies.
type RequestLogDetail struct {
	RequestLogSummary
	RequestBody  string
	ResponseBody string
}

// CostParams controls filtering for cost queries.
type CostParams struct {
	From      string // Start date (inclusive), e.g. "2026-03-01"
	To        string // End date (inclusive), e.g. "2026-03-11"
	GroupBy   string // "model" or "key"
	KeyPrefix string // Optional filter by proxy key prefix
}

// CostSummary holds aggregated cost totals for a date range.
type CostSummary struct {
	TotalCostUSD             float64
	TotalInputTokens         int64
	TotalOutputTokens        int64
	TotalCacheCreationTokens int64
	TotalCacheReadTokens     int64
	TotalRequests            int64
}

// CostBreakdownEntry holds cost aggregated by a grouping dimension.
type CostBreakdownEntry struct {
	Group               string
	CostUSD             float64
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	Requests            int64
}

// CostTimeseriesEntry holds cost data for a single time bucket.
type CostTimeseriesEntry struct {
	Date     string
	CostUSD  float64
	Requests int64
}

// AuditEvent records an administrative action for the audit trail.
type AuditEvent struct {
	ID        int64
	Action    string
	KeyPrefix string
	Detail    string
	CreatedAt time.Time
}
