// Package provider defines the interface and shared types for upstream LLM providers.
package provider

import (
	"context"
	"io"
	"net/http"
)

const (
	// MaxUpstreamResponseBytes is the maximum number of bytes to read from a
	// non-streaming upstream response body. This prevents OOM from malformed or
	// adversarial responses.
	MaxUpstreamResponseBytes = 32 << 20 // 32 MB

	// MaxOAuthResponseBytes is the maximum size for OAuth/error response bodies.
	MaxOAuthResponseBytes = 1 << 20 // 1 MB
)

// Request holds the data needed to forward a call to an upstream LLM provider.
type Request struct {
	Body        []byte
	Headers     http.Header
	Model       string // upstream model name
	IsStreaming bool
}

// Response carries the upstream provider's reply back to the proxy layer.
type Response struct {
	StatusCode               int
	Headers                  http.Header
	Body                     []byte        // for non-streaming
	Stream                   io.ReadCloser // for streaming (caller must close)
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	ReasoningTokens          int64
}

// Provider abstracts an upstream LLM service (e.g. Anthropic, OpenAI-compatible).
type Provider interface {
	// Name returns a short identifier for the provider (e.g. "anthropic").
	Name() string
	// AuthMode returns "proxy_key" or "forward" to indicate how the proxy
	// authenticates with the upstream service.
	AuthMode() string
	// APIFormat returns the native API format the provider speaks upstream.
	// Valid values include "anthropic", "openai", "responses", and "gemini".
	APIFormat() string
	// SendRequest forwards a translated request to the upstream provider.
	SendRequest(ctx context.Context, req *Request) (*Response, error)
}

// DiscoveredModel represents a single model returned by a provider's listing
// endpoint during automatic model discovery at startup.
type DiscoveredModel struct {
	ID          string // upstream model identifier (e.g., "claude-sonnet-4-6")
	DisplayName string // optional human-readable name
}

// ModelDiscoverer is an optional interface that providers can implement to
// support automatic model discovery at startup. Providers that implement this
// interface can be queried for their available models via ListModels.
type ModelDiscoverer interface {
	ListModels(ctx context.Context) ([]DiscoveredModel, error)
}
