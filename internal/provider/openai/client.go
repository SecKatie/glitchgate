// SPDX-License-Identifier: AGPL-3.0-or-later

package openai

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

// Client implements the provider.Provider interface for OpenAI-compatible APIs.
type Client struct {
	name           string
	baseURL        string
	authMode       string // "proxy_key" or "forward"
	apiKey         string // used when authMode == "proxy_key"
	apiType        string // "chat_completions" or "responses"
	httpClient     *http.Client
	requestTimeout time.Duration
}

// NewClient creates an OpenAI provider client. Returns an error if
// baseURL is not a valid HTTP(S) URL.
func NewClient(name, baseURL, authMode, apiKey, apiType string) (*Client, error) {
	if err := provider.ValidateBaseURL(baseURL); err != nil {
		return nil, fmt.Errorf("openai provider %q: %w", name, err)
	}
	if apiType == "" {
		apiType = APITypeChatCompletions
	}
	return &Client{
		name:           name,
		baseURL:        strings.TrimRight(baseURL, "/"),
		authMode:       authMode,
		apiKey:         apiKey,
		apiType:        apiType,
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

// APIFormat returns "openai" for Chat Completions or "responses" for Responses API.
func (c *Client) APIFormat() string {
	if c.apiType == APITypeResponses {
		return "responses"
	}
	return "openai"
}

// SendRequest dispatches a request to the OpenAI-compatible API.
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

	var endpoint string
	switch c.apiType {
	case APITypeResponses:
		endpoint = c.endpointURL("/responses")
	default:
		endpoint = c.endpointURL("/chat/completions")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(req.Body)))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// Auth mode: either use the proxy's own API key or forward the client's.
	switch c.authMode {
	case "proxy_key":
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
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
			strings.EqualFold(hdr, "Authorization") {
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, provider.MaxUpstreamResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", c.name, err)
	}
	provResp.Body = body

	// Extract token usage from non-streaming response.
	if resp.StatusCode == http.StatusOK {
		c.extractTokens(body, provResp)
	}

	return provResp, nil
}

func (c *Client) endpointURL(suffix string) string {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return c.baseURL + "/v1" + suffix
	}

	basePath := strings.TrimSuffix(u.Path, "/")
	switch {
	case basePath == "":
		u.Path = "/v1" + suffix
	case strings.HasSuffix(basePath, "/v1"):
		u.Path = basePath + suffix
	default:
		u.Path = path.Join(basePath, suffix)
	}

	return u.String()
}

func shouldForwardHeader(hdr string) bool {
	switch {
	case strings.EqualFold(hdr, "Accept"),
		strings.EqualFold(hdr, "Accept-Language"),
		strings.EqualFold(hdr, "Chatgpt-Account-Id"),
		strings.EqualFold(hdr, "Originator"),
		strings.EqualFold(hdr, "Session_id"),
		strings.EqualFold(hdr, "User-Agent"),
		strings.EqualFold(hdr, "OpenAI-Beta"),
		strings.EqualFold(hdr, "OpenAI-Organization"),
		strings.EqualFold(hdr, "OpenAI-Project"),
		strings.EqualFold(hdr, "X-App"):
		return true
	case strings.HasPrefix(strings.ToLower(hdr), "x-codex-"):
		return true
	case strings.HasPrefix(strings.ToLower(hdr), "x-stainless-"):
		return true
	default:
		return false
	}
}

// extractTokens parses the response body and extracts token usage.
func (c *Client) extractTokens(body []byte, resp *provider.Response) {
	if c.apiType == APITypeResponses {
		var rr struct {
			Usage *struct {
				InputTokens        int64 `json:"input_tokens"`
				OutputTokens       int64 `json:"output_tokens"`
				InputTokensDetails *struct {
					CachedTokens int64 `json:"cached_tokens"`
				} `json:"input_tokens_details"`
				OutputTokensDetails *struct {
					ReasoningTokens int64 `json:"reasoning_tokens"`
				} `json:"output_tokens_details"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(body, &rr); err == nil && rr.Usage != nil {
			resp.InputTokens = rr.Usage.InputTokens
			resp.OutputTokens = rr.Usage.OutputTokens
			if rr.Usage.InputTokensDetails != nil {
				resp.CacheReadInputTokens = rr.Usage.InputTokensDetails.CachedTokens
				resp.InputTokens -= resp.CacheReadInputTokens
				if resp.InputTokens < 0 {
					resp.InputTokens = 0
				}
			}
			if rr.Usage.OutputTokensDetails != nil {
				resp.ReasoningTokens = rr.Usage.OutputTokensDetails.ReasoningTokens
			}
		}
		return
	}

	// Chat Completions format.
	var cc struct {
		Usage *struct {
			PromptTokens        int64 `json:"prompt_tokens"`
			CompletionTokens    int64 `json:"completion_tokens"`
			PromptTokensDetails *struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionTokensDetails *struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &cc); err == nil && cc.Usage != nil {
		resp.InputTokens = cc.Usage.PromptTokens
		resp.OutputTokens = cc.Usage.CompletionTokens
		if cc.Usage.PromptTokensDetails != nil {
			resp.CacheReadInputTokens = cc.Usage.PromptTokensDetails.CachedTokens
			resp.InputTokens -= resp.CacheReadInputTokens
			if resp.InputTokens < 0 {
				resp.InputTokens = 0
			}
		}
		if cc.Usage.CompletionTokensDetails != nil {
			resp.ReasoningTokens = cc.Usage.CompletionTokensDetails.ReasoningTokens
		}
	}
}
