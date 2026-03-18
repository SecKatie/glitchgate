// SPDX-License-Identifier: AGPL-3.0-or-later

// Package gemini implements the provider.Provider interface for the Google
// Gemini Developer API authenticated with API keys.
package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/provider"
)

// DefaultBaseURL is the base URL for the Gemini Developer API.
const DefaultBaseURL = "https://generativelanguage.googleapis.com"

// Client implements provider.Provider for the Gemini Developer API.
type Client struct {
	name           string
	baseURL        string
	authMode       string // "proxy_key" or "forward"
	apiKey         string // used when authMode == "proxy_key"
	httpClient     *http.Client
	requestTimeout time.Duration
}

// NewClient creates a Gemini provider client. Returns an error if baseURL is
// not a valid HTTP(S) URL.
func NewClient(name, baseURL, authMode, apiKey string) (*Client, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if err := provider.ValidateBaseURL(baseURL); err != nil {
		return nil, fmt.Errorf("gemini provider %q: %w", name, err)
	}
	return &Client{
		name:           name,
		baseURL:        strings.TrimRight(baseURL, "/"),
		authMode:       authMode,
		apiKey:         apiKey,
		httpClient:     provider.BuildHTTPClient(),
		requestTimeout: config.DefaultUpstreamRequestTimeout,
	}, nil
}

// SetTimeouts overrides the default upstream request deadline.
func (c *Client) SetTimeouts(requestTimeout time.Duration) {
	c.requestTimeout = requestTimeout
}

// Name returns the provider's short identifier.
func (c *Client) Name() string { return c.name }

// AuthMode returns "proxy_key" or "forward".
func (c *Client) AuthMode() string { return c.authMode }

// APIFormat returns "gemini" because this provider speaks the native Gemini API.
func (c *Client) APIFormat() string { return "gemini" }

// SendRequest dispatches a request to the Gemini generateContent endpoint.
func (c *Client) SendRequest(ctx context.Context, req *provider.Request) (*provider.Response, error) {
	if !req.IsStreaming {
		var cancel context.CancelFunc
		ctx, cancel = provider.ContextWithDefaultTimeout(ctx, c.requestTimeout)
		defer cancel()
		return c.send(ctx, req, false)
	}

	resp, err := c.send(ctx, req, true)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusBadRequest {
		return resp, nil
	}

	// Some Gemini Developer API setups reject streamGenerateContent with 400
	// while accepting the equivalent non-streaming request body. Retry once
	// against generateContent so the proxy can synthesize SSE for the client.
	nonStreamResp, retryErr := c.send(ctx, req, false)
	if retryErr == nil && nonStreamResp.StatusCode < http.StatusBadRequest {
		return nonStreamResp, nil
	}
	return resp, nil
}

func (c *Client) send(ctx context.Context, req *provider.Request, streaming bool) (*provider.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpointURL(req.Model, streaming), strings.NewReader(string(req.Body)))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	switch c.authMode {
	case "proxy_key":
		httpReq.Header.Set("X-Goog-Api-Key", c.apiKey)
	case "forward":
		if apiKey := req.Headers.Get("X-Goog-Api-Key"); apiKey != "" {
			httpReq.Header.Set("X-Goog-Api-Key", apiKey)
		}
	}

	for hdr, values := range req.Headers {
		if !shouldForwardHeader(hdr) {
			continue
		}
		if strings.EqualFold(hdr, "Content-Type") ||
			strings.EqualFold(hdr, "X-Goog-Api-Key") {
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

	if streaming && resp.StatusCode < http.StatusBadRequest {
		provResp.Stream = resp.Body
		return provResp, nil
	}

	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			slog.Warn("closing response body", "error", cerr)
		}
	}()
	body, err := io.ReadAll(io.LimitReader(resp.Body, provider.MaxUpstreamResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", c.name, err)
	}
	provResp.Body = body

	if resp.StatusCode == http.StatusOK {
		extractGeminiTokens(body, provResp)
	}

	return provResp, nil
}

func (c *Client) endpointURL(model string, streaming bool) string {
	op := "generateContent"
	if streaming {
		op = "streamGenerateContent"
	}

	model = strings.TrimPrefix(model, "google/")

	u, err := url.Parse(c.baseURL)
	if err != nil {
		return c.baseURL + "/v1beta/models/" + model + ":" + op
	}

	basePath := strings.TrimSuffix(u.Path, "/")
	switch {
	case basePath == "":
		u.Path = path.Join("/", "v1beta", "models", model) + ":" + op
	case strings.HasSuffix(basePath, "/v1beta"):
		u.Path = path.Join(basePath, "models", model) + ":" + op
	default:
		u.Path = path.Join(basePath, "v1beta", "models", model) + ":" + op
	}

	if streaming {
		query := u.Query()
		query.Set("alt", "sse")
		u.RawQuery = query.Encode()
	}

	return u.String()
}

func shouldForwardHeader(hdr string) bool {
	switch {
	case strings.EqualFold(hdr, "Accept"),
		strings.EqualFold(hdr, "Accept-Language"),
		strings.EqualFold(hdr, "User-Agent"),
		strings.EqualFold(hdr, "X-App"):
		return true
	case strings.HasPrefix(strings.ToLower(hdr), "x-goog-"):
		return true
	default:
		return false
	}
}

func extractGeminiTokens(body []byte, resp *provider.Response) {
	var gr GeminiResponse
	if err := json.Unmarshal(body, &gr); err == nil && gr.UsageMetadata != nil {
		resp.InputTokens, resp.OutputTokens, resp.CacheReadInputTokens, resp.ReasoningTokens = GeminiUsageTotals(gr.UsageMetadata)
	}
}
