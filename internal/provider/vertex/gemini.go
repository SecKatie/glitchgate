// SPDX-License-Identifier: AGPL-3.0-or-later

package vertex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/provider"
	"github.com/seckatie/glitchgate/internal/translate"
)

// GeminiClient implements provider.Provider for Gemini models on Vertex AI
// using the native generateContent / streamGenerateContent endpoints.
type GeminiClient struct {
	name           string
	project        string
	region         string
	httpClient     *http.Client
	requestTimeout time.Duration
	tokenSource    oauth2.TokenSource
}

// NewGeminiClient creates a Vertex AI provider client for Gemini models.
// If credentialsFile is non-empty, it reads that file for service account JSON;
// otherwise it uses Application Default Credentials (ADC).
func NewGeminiClient(name, project, region, credentialsFile string) (*GeminiClient, error) {
	ts, err := newTokenSource(name, credentialsFile)
	if err != nil {
		return nil, err
	}

	if region == "" {
		region = "us-central1"
	}

	return &GeminiClient{
		name:           name,
		project:        project,
		region:         region,
		httpClient:     provider.BuildHTTPClient(),
		requestTimeout: config.DefaultUpstreamRequestTimeout,
		tokenSource:    ts,
	}, nil
}

// newTestGeminiClient creates a GeminiClient with a custom token source and
// HTTP client, for use in unit tests only.
func newTestGeminiClient(name, project, region string, ts oauth2.TokenSource, httpClient *http.Client) *GeminiClient {
	if region == "" {
		region = "us-central1"
	}
	return &GeminiClient{
		name:           name,
		project:        project,
		region:         region,
		httpClient:     httpClient,
		requestTimeout: config.DefaultUpstreamRequestTimeout,
		tokenSource:    ts,
	}
}

// SetTimeouts overrides the default upstream request deadline.
func (c *GeminiClient) SetTimeouts(requestTimeout time.Duration) {
	c.requestTimeout = requestTimeout
}

// Name returns the provider's short identifier.
func (c *GeminiClient) Name() string { return c.name }

// AuthMode returns "internal" — this provider manages its own OAuth2 auth.
func (c *GeminiClient) AuthMode() string { return "internal" }

// APIFormat returns "gemini" — Vertex Gemini speaks the native Gemini API.
func (c *GeminiClient) APIFormat() string { return "gemini" }

// SendRequest dispatches a request to the Vertex AI Gemini native endpoint.
// The request body must be in Gemini generateContent format (translation
// happens in the proxy pipeline before reaching this method).
func (c *GeminiClient) SendRequest(ctx context.Context, req *provider.Request) (*provider.Response, error) {
	if !req.IsStreaming {
		var cancel context.CancelFunc
		ctx, cancel = provider.ContextWithDefaultTimeout(ctx, c.requestTimeout)
		defer cancel()
	}

	token, err := c.tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("vertex gemini provider %q: obtaining access token: %w", c.name, err)
	}

	// Strip "google/" prefix from model name for the URL path.
	model := strings.TrimPrefix(req.Model, "google/")

	method := "generateContent"
	if req.IsStreaming {
		method = "streamGenerateContent?alt=sse"
	}
	url := fmt.Sprintf(
		"https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:%s",
		c.region, c.project, c.region, model, method,
	)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(req.Body)))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token.AccessToken)

	resp, err := c.httpClient.Do(httpReq) // #nosec G704 -- URL is constructed from operator-controlled config (project, region)
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
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", c.name, err)
	}
	provResp.Body = respBody

	if resp.StatusCode == http.StatusOK {
		extractGeminiTokens(respBody, provResp)
	}

	return provResp, nil
}

// extractGeminiTokens parses Gemini usageMetadata from a response body.
func extractGeminiTokens(body []byte, resp *provider.Response) {
	var gr translate.GeminiResponse
	if err := json.Unmarshal(body, &gr); err == nil && gr.UsageMetadata != nil {
		resp.InputTokens, resp.OutputTokens, resp.CacheReadInputTokens, resp.ReasoningTokens = translate.GeminiUsageTotals(gr.UsageMetadata)
	}
}
