// SPDX-License-Identifier: AGPL-3.0-or-later

// Package anthropic implements the provider.Provider interface for the Anthropic Messages API.
package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"codeberg.org/kglitchy/glitchgate/internal/config"
	"codeberg.org/kglitchy/glitchgate/internal/provider"
)

// Client implements the provider.Provider interface for the Anthropic Messages API.
type Client struct {
	name           string
	baseURL        string
	authMode       string // "proxy_key" or "forward"
	apiKey         string // used when authMode == "proxy_key"
	defaultVersion string
	httpClient     *http.Client
	requestTimeout time.Duration
}

// NewClient creates an Anthropic provider client.
func NewClient(name, baseURL, authMode, apiKey, defaultVersion string) *Client {
	return &Client{
		name:           name,
		baseURL:        strings.TrimRight(baseURL, "/"),
		authMode:       authMode,
		apiKey:         apiKey,
		defaultVersion: defaultVersion,
		httpClient:     provider.BuildHTTPClient(),
		requestTimeout: config.DefaultUpstreamRequestTimeout,
	}
}

// SetTimeouts overrides the default upstream request deadline.
func (c *Client) SetTimeouts(requestTimeout time.Duration) {
	c.requestTimeout = requestTimeout
}

// Name returns the provider's short identifier.
func (c *Client) Name() string { return c.name }

// AuthMode returns "proxy_key" or "forward" indicating how the proxy authenticates upstream.
func (c *Client) AuthMode() string { return c.authMode }

// APIFormat returns "anthropic" — this provider speaks the Anthropic Messages API natively.
func (c *Client) APIFormat() string { return "anthropic" }

// SendRequest dispatches a request to the Anthropic Messages API.
// For streaming requests, Response.Stream is set and the caller must close it.
// For non-streaming requests, Response.Body is populated.
func (c *Client) SendRequest(ctx context.Context, req *provider.Request) (*provider.Response, error) {
	// For streaming requests, don't apply a request timeout that would cancel
	// the context when this function returns. The stream continues to be read
	// after SendRequest completes. Rely on ResponseHeaderTimeout for the initial
	// connection, and let the stream remain open until the server completes.
	if !req.IsStreaming {
		var cancel context.CancelFunc
		ctx, cancel = provider.ContextWithDefaultTimeout(ctx, c.requestTimeout)
		defer cancel()
	}

	url := c.baseURL + "/v1/messages"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(req.Body)))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// Set the anthropic-version header.
	version := req.Headers.Get("Anthropic-Version")
	if version == "" {
		version = c.defaultVersion
	}
	if version != "" {
		httpReq.Header.Set("Anthropic-Version", version)
	}

	// Auth mode: either use the proxy's own API key or forward the client's.
	switch c.authMode {
	case "proxy_key":
		httpReq.Header.Set("X-Api-Key", c.apiKey)
	case "forward":
		if auth := req.Headers.Get("Authorization"); auth != "" {
			httpReq.Header.Set("Authorization", auth)
		}
	}

	for hdr, values := range req.Headers {
		if !shouldForwardHeader(hdr) {
			continue
		}
		if strings.EqualFold(hdr, "Content-Type") ||
			strings.EqualFold(hdr, "Anthropic-Version") ||
			strings.EqualFold(hdr, "Authorization") ||
			strings.EqualFold(hdr, "X-Api-Key") {
			continue
		}
		for _, v := range values {
			httpReq.Header.Add(hdr, v)
		}
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending request to %s: %w", c.name, err)
	}

	provResp := &provider.Response{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
	}

	if req.IsStreaming {
		provResp.Stream = resp.Body
		return provResp, nil
	}

	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			slog.Warn("closing response body", "error", cerr)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", c.name, err)
	}
	provResp.Body = body

	// Extract token usage from non-streaming response.
	if resp.StatusCode == http.StatusOK {
		var msgResp MessagesResponse
		if err := json.Unmarshal(body, &msgResp); err == nil {
			provResp.InputTokens = msgResp.Usage.InputTokens
			provResp.OutputTokens = msgResp.Usage.OutputTokens
			provResp.CacheCreationInputTokens = msgResp.Usage.CacheCreationInputTokens
			provResp.CacheReadInputTokens = msgResp.Usage.CacheReadInputTokens
		}
	}

	return provResp, nil
}

func shouldForwardHeader(hdr string) bool {
	switch {
	case strings.EqualFold(hdr, "Accept"),
		strings.EqualFold(hdr, "User-Agent"),
		strings.EqualFold(hdr, "X-App"):
		return true
	case strings.HasPrefix(strings.ToLower(hdr), "anthropic-"):
		return true
	case strings.HasPrefix(strings.ToLower(hdr), "x-stainless-"):
		return true
	default:
		return false
	}
}
