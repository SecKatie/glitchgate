// SPDX-License-Identifier: AGPL-3.0-or-later

// Package anthropic implements the provider.Provider interface for the Anthropic Messages API.
package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"codeberg.org/kglitchy/llm-proxy/internal/provider"
)

// Client implements the provider.Provider interface for the Anthropic Messages API.
type Client struct {
	name           string
	baseURL        string
	authMode       string // "proxy_key" or "forward"
	apiKey         string // used when authMode == "proxy_key"
	defaultVersion string
	httpClient     *http.Client
}

// NewClient creates an Anthropic provider client.
func NewClient(name, baseURL, authMode, apiKey, defaultVersion string) *Client {
	return &Client{
		name:           name,
		baseURL:        strings.TrimRight(baseURL, "/"),
		authMode:       authMode,
		apiKey:         apiKey,
		defaultVersion: defaultVersion,
		httpClient:     &http.Client{},
	}
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

	// Forward any additional Anthropic-specific headers.
	for _, hdr := range []string{"Anthropic-Beta"} {
		if v := req.Headers.Get(hdr); v != "" {
			httpReq.Header.Set(hdr, v)
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

	defer func() { _ = resp.Body.Close() }()
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
