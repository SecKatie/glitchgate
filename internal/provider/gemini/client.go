// SPDX-License-Identifier: AGPL-3.0-or-later

// Package gemini implements the provider.Provider interface for the Google
// Gemini API, supporting both the Developer API (API key auth) and Vertex AI
// (OAuth2 auth).
package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/provider"
)

const (
	developerAPIBaseURL = "https://generativelanguage.googleapis.com"
	cloudPlatformScope  = "https://www.googleapis.com/auth/cloud-platform"
	defaultVertexRegion = "us-central1"
)

// ClientConfig holds the parameters needed to construct a Gemini [Client].
type ClientConfig struct {
	Name            string
	AuthMode        string // "api_key" or "vertex"
	APIKey          string // required when AuthMode == "api_key"
	Project         string // required when AuthMode == "vertex"
	Region          string // optional for vertex; defaults to us-central1
	CredentialsFile string // optional for vertex; uses ADC when empty
}

// Client implements provider.Provider for the Google Gemini API.
type Client struct {
	name           string
	authMode       string             // "api_key" or "vertex"
	apiKey         string             // api_key mode
	tokenSource    oauth2.TokenSource // vertex mode
	project        string             // vertex mode
	region         string             // vertex mode
	httpClient     *http.Client
	requestTimeout time.Duration
}

// NewClient creates a Gemini provider client configured for either API key or
// Vertex AI authentication.
func NewClient(cfg ClientConfig) (*Client, error) {
	c := &Client{
		name:           cfg.Name,
		authMode:       cfg.AuthMode,
		apiKey:         cfg.APIKey,
		project:        cfg.Project,
		region:         cfg.Region,
		httpClient:     provider.BuildHTTPClient(),
		requestTimeout: config.DefaultUpstreamRequestTimeout,
	}

	if c.authMode == "vertex" {
		if c.region == "" {
			c.region = defaultVertexRegion
		}
		ts, err := newTokenSource(cfg.Name, cfg.CredentialsFile)
		if err != nil {
			return nil, err
		}
		c.tokenSource = ts
	}

	return c, nil
}

// newTestClient creates a Client with a custom token source and HTTP client,
// for use in unit tests only.
func newTestClient(cfg ClientConfig, ts oauth2.TokenSource, httpClient *http.Client) *Client {
	region := cfg.Region
	if cfg.AuthMode == "vertex" && region == "" {
		region = defaultVertexRegion
	}
	return &Client{
		name:           cfg.Name,
		authMode:       cfg.AuthMode,
		apiKey:         cfg.APIKey,
		tokenSource:    ts,
		project:        cfg.Project,
		region:         region,
		httpClient:     httpClient,
		requestTimeout: config.DefaultUpstreamRequestTimeout,
	}
}

// SetTimeouts overrides the default upstream request deadline.
func (c *Client) SetTimeouts(requestTimeout time.Duration) {
	c.requestTimeout = requestTimeout
}

// Name returns the provider's short identifier.
func (c *Client) Name() string { return c.name }

// AuthMode returns "api_key" or "internal" (vertex manages its own OAuth2 auth).
func (c *Client) AuthMode() string {
	if c.authMode == "vertex" {
		return "internal"
	}
	return c.authMode
}

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

	// Some Gemini Developer API setups reject streamGenerateContent with 400
	// while accepting the equivalent non-streaming request body. Retry once
	// against generateContent so the proxy can synthesize SSE for the client.
	// This quirk does not apply to Vertex AI.
	if c.authMode == "api_key" && resp.StatusCode == http.StatusBadRequest {
		nonStreamResp, retryErr := c.send(ctx, req, false)
		if retryErr == nil && nonStreamResp.StatusCode < http.StatusBadRequest {
			return nonStreamResp, nil
		}
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
	case "api_key":
		httpReq.Header.Set("X-Goog-Api-Key", c.apiKey)
	case "vertex":
		token, tokenErr := c.tokenSource.Token()
		if tokenErr != nil {
			return nil, fmt.Errorf("gemini provider %q: obtaining access token: %w", c.name, tokenErr)
		}
		httpReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
	}

	resp, err := c.httpClient.Do(httpReq) //nolint:gosec // URL from operator-controlled provider config, not user input
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
	model = strings.TrimPrefix(model, "google/")

	switch c.authMode {
	case "vertex":
		method := "generateContent"
		if streaming {
			method = "streamGenerateContent?alt=sse"
		}
		return fmt.Sprintf(
			"https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:%s",
			c.region, c.project, c.region, model, method,
		)
	default: // api_key
		op := "generateContent"
		if streaming {
			op = "streamGenerateContent"
		}
		u := developerAPIBaseURL + "/v1beta/models/" + model + ":" + op
		if streaming {
			u += "?alt=sse"
		}
		return u
	}
}

func extractGeminiTokens(body []byte, resp *provider.Response) {
	var gr Response
	if err := json.Unmarshal(body, &gr); err == nil && gr.UsageMetadata != nil {
		resp.InputTokens, resp.OutputTokens, resp.CacheReadInputTokens, resp.ReasoningTokens = UsageTotals(gr.UsageMetadata)
	}
}

// newTokenSource creates an OAuth2 token source from either an explicit
// credentials file or Application Default Credentials (ADC). The returned
// token source handles automatic refresh.
func newTokenSource(providerName, credentialsFile string) (oauth2.TokenSource, error) {
	ctx := context.Background()

	var ts oauth2.TokenSource
	if credentialsFile != "" {
		data, err := os.ReadFile(credentialsFile) // #nosec G304 -- path comes from operator-controlled config
		if err != nil {
			return nil, fmt.Errorf("gemini provider %q: reading credentials file: %w", providerName, err)
		}
		creds, err := google.CredentialsFromJSON(ctx, data, cloudPlatformScope) //nolint:staticcheck // SA1019: credentials_file path is operator-controlled config, not untrusted input
		if err != nil {
			return nil, fmt.Errorf("gemini provider %q: parsing credentials: %w", providerName, err)
		}
		ts = creds.TokenSource
	} else {
		creds, err := google.FindDefaultCredentials(ctx, cloudPlatformScope)
		if err != nil {
			return nil, fmt.Errorf("gemini provider %q: finding default credentials: %w", providerName, err)
		}
		ts = creds.TokenSource
	}

	return oauth2.ReuseTokenSource(nil, ts), nil
}

// ListModels queries the provider's model listing endpoint and returns all
// available models that support generateContent. For direct API key mode
// it calls GET /v1beta/models. For Vertex mode it calls the publisher models endpoint.
func (c *Client) ListModels(ctx context.Context) ([]provider.DiscoveredModel, error) {
	if c.authMode == "vertex" {
		return c.listModelsVertex(ctx)
	}
	return c.listModelsDirect(ctx)
}

func (c *Client) listModelsDirect(ctx context.Context) ([]provider.DiscoveredModel, error) {
	var all []provider.DiscoveredModel
	pageToken := ""

	for {
		u := developerAPIBaseURL + "/v1beta/models?pageSize=1000&key=" + c.apiKey
		if pageToken != "" {
			u += "&pageToken=" + pageToken
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("gemini provider %q: creating list request: %w", c.name, err)
		}

		resp, err := c.httpClient.Do(req) // #nosec G107 -- URL from operator-controlled provider config
		if err != nil {
			return nil, fmt.Errorf("gemini provider %q: listing models: %w", c.name, err)
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, provider.MaxUpstreamResponseBytes))
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("gemini provider %q: reading models response: %w", c.name, err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("gemini provider %q: models endpoint returned %d: %s", c.name, resp.StatusCode, string(body))
		}

		var listResp geminiModelsListResponse
		if err := json.Unmarshal(body, &listResp); err != nil {
			return nil, fmt.Errorf("gemini provider %q: decoding models response: %w", c.name, err)
		}

		for _, m := range listResp.Models {
			if !supportsGenerateContent(m.SupportedGenerationMethods) {
				continue
			}
			id := strings.TrimPrefix(m.Name, "models/")
			all = append(all, provider.DiscoveredModel{
				ID:          id,
				DisplayName: m.DisplayName,
			})
		}

		if listResp.NextPageToken == "" {
			break
		}
		pageToken = listResp.NextPageToken
	}

	return all, nil
}

func (c *Client) listModelsVertex(ctx context.Context) ([]provider.DiscoveredModel, error) {
	token, err := c.tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("gemini provider %q: obtaining access token: %w", c.name, err)
	}

	var all []provider.DiscoveredModel
	pageToken := ""

	for {
		u := fmt.Sprintf(
			"https://%s-aiplatform.googleapis.com/v1beta1/publishers/google/models?pageSize=100",
			c.region,
		)
		if pageToken != "" {
			u += "&pageToken=" + pageToken
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("gemini provider %q: creating vertex list request: %w", c.name, err)
		}
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)

		resp, err := c.httpClient.Do(req) // #nosec G107 -- URL from operator-controlled provider config
		if err != nil {
			return nil, fmt.Errorf("gemini provider %q: listing vertex models: %w", c.name, err)
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, provider.MaxUpstreamResponseBytes))
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("gemini provider %q: reading vertex models response: %w", c.name, err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("gemini provider %q: vertex models endpoint returned %d: %s", c.name, resp.StatusCode, string(body))
		}

		var listResp vertexGeminiModelsListResponse
		if err := json.Unmarshal(body, &listResp); err != nil {
			return nil, fmt.Errorf("gemini provider %q: decoding vertex models response: %w", c.name, err)
		}

		for _, m := range listResp.PublisherModels {
			parts := strings.SplitN(m.Name, "/", 4)
			if len(parts) == 4 {
				all = append(all, provider.DiscoveredModel{ID: parts[3]})
			}
		}

		if listResp.NextPageToken == "" {
			break
		}
		pageToken = listResp.NextPageToken
	}

	return all, nil
}

func supportsGenerateContent(methods []string) bool {
	for _, m := range methods {
		if m == "generateContent" {
			return true
		}
	}
	return false
}
