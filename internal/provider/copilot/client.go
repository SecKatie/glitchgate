// SPDX-License-Identifier: AGPL-3.0-or-later

package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/provider"
)

// Client implements the provider.Provider interface for the GitHub Copilot API.
type Client struct {
	name     string
	tokenDir string

	mu             sync.Mutex
	githubToken    *GitHubToken
	sessionToken   *SessionToken
	httpClient     *http.Client
	requestTimeout time.Duration
}

// NewClient creates a Copilot provider client.
// It reads stored tokens from tokenDir immediately.
func NewClient(name, tokenDir string) *Client {
	c := &Client{
		name:           name,
		tokenDir:       tokenDir,
		httpClient:     provider.BuildHTTPClient(),
		requestTimeout: config.DefaultUpstreamRequestTimeout,
	}

	// Attempt to load cached tokens at construction time.
	if gt, err := LoadGitHubToken(tokenDir); err == nil {
		c.githubToken = gt
	}
	if st, err := LoadCopilotToken(tokenDir); err == nil {
		c.sessionToken = st
	}

	return c
}

// SetTimeouts overrides the default upstream request deadline.
func (c *Client) SetTimeouts(requestTimeout time.Duration) {
	c.requestTimeout = requestTimeout
}

// Name returns the provider's short identifier.
func (c *Client) Name() string { return c.name }

// AuthMode returns "internal" — the Copilot provider manages its own authentication.
func (c *Client) AuthMode() string { return "internal" }

// APIFormat returns "openai" — the Copilot API speaks OpenAI Chat Completions format.
func (c *Client) APIFormat() string { return "openai" }

// SendRequest dispatches a request to the GitHub Copilot chat completions API.
func (c *Client) SendRequest(ctx context.Context, req *provider.Request) (*provider.Response, error) {
	// For non-streaming requests apply the configured deadline. Streaming
	// requests must not have a context deadline: the transport's
	// ResponseHeaderTimeout guards the initial connection phase and, once
	// headers are received, the stream stays open until the server finishes
	// or the client disconnects.
	if !req.IsStreaming {
		var cancel context.CancelFunc
		ctx, cancel = provider.ContextWithDefaultTimeout(ctx, c.requestTimeout)
		defer cancel()
	}

	sessionToken, apiBase, err := c.getSessionToken(ctx)
	if err != nil {
		return nil, err
	}

	url := strings.TrimRight(apiBase, "/") + "/chat/completions"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(req.Body)))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// Required headers.
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+sessionToken)
	httpReq.Header.Set("Editor-Version", editorVersion)
	httpReq.Header.Set("Editor-Plugin-Version", editorPluginVersion)
	httpReq.Header.Set("Copilot-Integration-Id", copilotIntegrationID)
	httpReq.Header.Set("User-Agent", copilotUserAgent)

	resp, err := c.httpClient.Do(httpReq) //nolint:gosec // URL from validated provider config, not user input
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
		var chatResp ChatCompletionResponse
		if err := json.Unmarshal(body, &chatResp); err == nil && chatResp.Usage != nil {
			provResp.InputTokens = chatResp.Usage.PromptTokens
			provResp.OutputTokens = chatResp.Usage.CompletionTokens
			if chatResp.Usage.PromptTokensDetails != nil {
				provResp.CacheReadInputTokens = chatResp.Usage.PromptTokensDetails.CachedTokens
				provResp.InputTokens -= provResp.CacheReadInputTokens
				if provResp.InputTokens < 0 {
					provResp.InputTokens = 0
				}
			}
			if chatResp.Usage.CompletionTokensDetails != nil {
				provResp.ReasoningTokens = chatResp.Usage.CompletionTokensDetails.ReasoningTokens
			}
		}
	}

	return provResp, nil
}

// getSessionToken returns a valid Copilot session token, refreshing if expired.
func (c *Client) getSessionToken(ctx context.Context) (token string, apiBase string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.githubToken == nil {
		return "", "", fmt.Errorf(
			"no GitHub Copilot credentials found — run: glitchgate auth copilot")
	}

	// Return cached session token if still valid (with 60s buffer).
	if c.sessionToken != nil && c.sessionToken.Token != "" {
		if time.Now().Unix() < c.sessionToken.ExpiresAt-60 {
			return c.sessionToken.Token, c.sessionToken.APIBase, nil
		}
	}

	// Refresh the session token.
	st, err := ExchangeForCopilotToken(ctx, c.githubToken.AccessToken)
	if err != nil {
		return "", "", fmt.Errorf("refreshing Copilot session token: %w", err)
	}
	c.sessionToken = st

	// Best-effort cache to disk.
	_ = SaveCopilotToken(c.tokenDir, st)

	return st.Token, st.APIBase, nil
}
