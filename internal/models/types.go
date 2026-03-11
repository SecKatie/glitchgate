// Package models defines shared internal data types used across packages.
package models

import "time"

// RequestLog records a single proxied API call with usage and cost metadata.
type RequestLog struct {
	ID               string
	ProxyKeyID       string
	Timestamp        time.Time
	SourceFormat     string // "anthropic" or "openai"
	ProviderName     string
	ModelRequested   string
	ModelUpstream    string
	InputTokens      int64
	OutputTokens     int64
	LatencyMs        int64
	Status           int
	RequestBody      string
	ResponseBody     string
	EstimatedCostUSD *float64 // nil if unknown
	ErrorDetails     *string
	IsStreaming      bool
}

// ProxyKey represents a hashed API key used to authenticate callers to the proxy.
type ProxyKey struct {
	ID        string
	KeyHash   string
	KeyPrefix string
	Label     string
	CreatedAt time.Time
	RevokedAt *time.Time
}
