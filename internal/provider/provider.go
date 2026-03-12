// Package provider defines the interface and shared types for upstream LLM providers.
package provider

import (
	"context"
	"io"
	"net/http"
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
}

// Provider abstracts an upstream LLM service (e.g. Anthropic, OpenAI-compatible).
type Provider interface {
	// Name returns a short identifier for the provider (e.g. "anthropic").
	Name() string
	// AuthMode returns "proxy_key" or "forward" to indicate how the proxy
	// authenticates with the upstream service.
	AuthMode() string
	// APIFormat returns the native API format the provider speaks upstream.
	// Valid values: "anthropic" (Anthropic Messages API) or "openai" (OpenAI Chat Completions API).
	APIFormat() string
	// SendRequest forwards a translated request to the upstream provider.
	SendRequest(ctx context.Context, req *Request) (*Response, error)
}
