// SPDX-License-Identifier: AGPL-3.0-or-later

// Package anthropic implements the provider.Provider interface for the
// Anthropic Messages API, supporting both the direct Anthropic API and
// Vertex AI (OAuth2 auth).
package anthropic

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
	cloudPlatformScope   = "https://www.googleapis.com/auth/cloud-platform"
	defaultVertexVersion = "vertex-2023-10-16"
	defaultVertexRegion  = "us-central1"
)

// ClientConfig holds the parameters needed to construct an Anthropic [Client].
type ClientConfig struct {
	Name            string
	BaseURL         string // required for api_key/forward modes
	AuthMode        string // "api_key", "forward", or "vertex"
	APIKey          string // api_key mode
	DefaultVersion  string
	Project         string // vertex mode
	Region          string // vertex mode; defaults to us-central1
	CredentialsFile string // vertex mode; empty = ADC
}

// Client implements the provider.Provider interface for the Anthropic Messages API.
type Client struct {
	name           string
	baseURL        string
	authMode       string // "api_key", "forward", or "vertex"
	apiKey         string // api_key mode
	defaultVersion string
	tokenSource    oauth2.TokenSource // vertex mode
	project        string             // vertex mode
	region         string             // vertex mode
	httpClient     *http.Client
	requestTimeout time.Duration
}

// NewClient creates an Anthropic provider client. For proxy_key/forward modes
// it validates the base URL. For vertex mode it initialises an OAuth2 token
// source.
func NewClient(cfg ClientConfig) (*Client, error) {
	c := &Client{
		name:           cfg.Name,
		baseURL:        strings.TrimRight(cfg.BaseURL, "/"),
		authMode:       cfg.AuthMode,
		apiKey:         cfg.APIKey,
		defaultVersion: cfg.DefaultVersion,
		project:        cfg.Project,
		region:         cfg.Region,
		httpClient:     provider.BuildHTTPClient(),
		requestTimeout: config.DefaultUpstreamRequestTimeout,
	}

	switch cfg.AuthMode {
	case "vertex":
		if c.region == "" {
			c.region = defaultVertexRegion
		}
		if c.defaultVersion == "" {
			c.defaultVersion = defaultVertexVersion
		}
		ts, err := newTokenSource(cfg.Name, cfg.CredentialsFile)
		if err != nil {
			return nil, err
		}
		c.tokenSource = ts
	default:
		if err := provider.ValidateBaseURL(cfg.BaseURL); err != nil {
			return nil, fmt.Errorf("anthropic provider %q: %w", cfg.Name, err)
		}
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
	defaultVersion := cfg.DefaultVersion
	if cfg.AuthMode == "vertex" && defaultVersion == "" {
		defaultVersion = defaultVertexVersion
	}
	return &Client{
		name:           cfg.Name,
		baseURL:        strings.TrimRight(cfg.BaseURL, "/"),
		authMode:       cfg.AuthMode,
		apiKey:         cfg.APIKey,
		defaultVersion: defaultVersion,
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

// AuthMode returns "api_key", "forward", or "internal" (vertex manages its own OAuth2 auth).
func (c *Client) AuthMode() string {
	if c.authMode == "vertex" {
		return "internal"
	}
	return c.authMode
}

// APIFormat returns "anthropic" — this provider speaks the Anthropic Messages API natively.
func (c *Client) APIFormat() string { return "anthropic" }

// SendRequest dispatches a request to the Anthropic Messages API.
// For streaming requests, Response.Stream is set and the caller must close it.
// For non-streaming requests, Response.Body is populated.
func (c *Client) SendRequest(ctx context.Context, req *provider.Request) (*provider.Response, error) {
	if !req.IsStreaming {
		var cancel context.CancelFunc
		ctx, cancel = provider.ContextWithDefaultTimeout(ctx, c.requestTimeout)
		defer cancel()
	}

	if c.authMode == "vertex" {
		return c.sendVertex(ctx, req)
	}
	return c.sendDirect(ctx, req)
}

// sendDirect handles api_key and forward auth modes against the Anthropic API.
func (c *Client) sendDirect(ctx context.Context, req *provider.Request) (*provider.Response, error) {
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
	case "api_key":
		// Anthropic's official API expects X-Api-Key header.
		// Third-party Anthropic-compatible APIs (like Minimax) expect Bearer prefix.
		if strings.Contains(c.baseURL, "minimax.io") {
			httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
		} else {
			httpReq.Header.Set("X-Api-Key", c.apiKey)
		}
	case "forward":
		if auth := req.Headers.Get("Authorization"); auth != "" {
			httpReq.Header.Set("Authorization", auth)
		}
	default:
		slog.Warn("unknown auth mode, falling back to api_key", "auth_mode", c.authMode)
		httpReq.Header.Set("Authorization", c.apiKey)
	}

	slog.Debug("sending request", "url", url, "auth_mode", c.authMode, "x_api_key", redactKey(httpReq.Header.Get("X-Api-Key")), "auth", redactKey(httpReq.Header.Get("Authorization"))) //nolint:gosec // structured slog prevents log injection

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

	return c.doAndParse(httpReq, req.IsStreaming)
}

// sendVertex handles Vertex AI auth and URL construction.
func (c *Client) sendVertex(ctx context.Context, req *provider.Request) (*provider.Response, error) {
	token, err := c.tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("anthropic provider %q: obtaining access token: %w", c.name, err)
	}

	body, err := prepareBody(req.Body, c.defaultVersion)
	if err != nil {
		return nil, fmt.Errorf("anthropic provider %q: preparing request body: %w", c.name, err)
	}

	method := "rawPredict"
	if req.IsStreaming {
		method = "streamRawPredict"
	}
	url := fmt.Sprintf(
		"https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/anthropic/models/%s:%s",
		c.region, c.project, c.region, req.Model, method,
	)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token.AccessToken)

	return c.doAndParse(httpReq, req.IsStreaming)
}

// doAndParse executes the HTTP request and parses the response into a
// provider.Response, extracting token usage for non-streaming 200 responses.
func (c *Client) doAndParse(httpReq *http.Request, streaming bool) (*provider.Response, error) {
	resp, err := c.httpClient.Do(httpReq) // #nosec G704 -- URL from operator-controlled provider config, not user input
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

// --- Vertex AI body preparation ---

// vertexUnsupportedFields lists Anthropic API fields that the Vertex AI
// rawPredict endpoint rejects with "Extra inputs are not permitted".
var vertexUnsupportedFields = []string{
	"context_management",
}

// prepareBody removes the "model" field (Vertex puts it in the URL), strips
// fields unsupported by Vertex, and injects "anthropic_version" if not present.
func prepareBody(raw []byte, defaultVersion string) ([]byte, error) {
	var bodyMap map[string]any
	if err := json.Unmarshal(raw, &bodyMap); err != nil {
		return nil, fmt.Errorf("parsing request body: %w", err)
	}

	delete(bodyMap, "model")
	for _, field := range vertexUnsupportedFields {
		delete(bodyMap, field)
	}

	if _, ok := bodyMap["anthropic_version"]; !ok {
		bodyMap["anthropic_version"] = defaultVersion
	}

	return json.Marshal(bodyMap)
}

// --- Header forwarding (direct API only) ---

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

// --- Auth helpers ---

// redactKey returns a redacted version of an API key for logging.
func redactKey(key string) string {
	if key == "" {
		return ""
	}
	const prefixLen = 7
	if len(key) <= prefixLen {
		return "***"
	}
	return key[:prefixLen] + "***"
}

// newTokenSource creates an OAuth2 token source from either an explicit
// credentials file or Application Default Credentials (ADC).
func newTokenSource(providerName, credentialsFile string) (oauth2.TokenSource, error) {
	ctx := context.Background()

	var ts oauth2.TokenSource
	if credentialsFile != "" {
		data, err := os.ReadFile(credentialsFile) // #nosec G304 -- path comes from operator-controlled config
		if err != nil {
			return nil, fmt.Errorf("anthropic provider %q: reading credentials file: %w", providerName, err)
		}
		creds, err := google.CredentialsFromJSON(ctx, data, cloudPlatformScope) //nolint:staticcheck // SA1019: credentials_file path is operator-controlled config, not untrusted input
		if err != nil {
			return nil, fmt.Errorf("anthropic provider %q: parsing credentials: %w", providerName, err)
		}
		ts = creds.TokenSource
	} else {
		creds, err := google.FindDefaultCredentials(ctx, cloudPlatformScope)
		if err != nil {
			return nil, fmt.Errorf("anthropic provider %q: finding default credentials: %w", providerName, err)
		}
		ts = creds.TokenSource
	}

	return oauth2.ReuseTokenSource(nil, ts), nil
}

// ListModels queries the provider's model listing endpoint and returns all
// available models. For direct API mode it calls GET /v1/models with cursor
// pagination. For Vertex mode it calls the publisher models endpoint.
func (c *Client) ListModels(ctx context.Context) ([]provider.DiscoveredModel, error) {
	if c.authMode == "vertex" {
		return c.listModelsVertex(ctx)
	}
	return c.listModelsDirect(ctx)
}

func (c *Client) listModelsDirect(ctx context.Context) ([]provider.DiscoveredModel, error) {
	var all []provider.DiscoveredModel
	afterID := ""

	for {
		u := c.baseURL + "/v1/models?limit=1000"
		if afterID != "" {
			u += "&after_id=" + afterID
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("anthropic provider %q: creating list request: %w", c.name, err)
		}
		req.Header.Set("X-Api-Key", c.apiKey)
		version := c.defaultVersion
		if version == "" {
			version = "2023-06-01"
		}
		req.Header.Set("Anthropic-Version", version)

		resp, err := c.httpClient.Do(req) // #nosec G107 -- URL from operator-controlled provider config
		if err != nil {
			return nil, fmt.Errorf("anthropic provider %q: listing models: %w", c.name, err)
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, provider.MaxUpstreamResponseBytes))
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("anthropic provider %q: reading models response: %w", c.name, err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("anthropic provider %q: models endpoint returned %d: %s", c.name, resp.StatusCode, string(body))
		}

		var listResp modelsListResponse
		if err := json.Unmarshal(body, &listResp); err != nil {
			return nil, fmt.Errorf("anthropic provider %q: decoding models response: %w", c.name, err)
		}

		for _, m := range listResp.Data {
			all = append(all, provider.DiscoveredModel{
				ID:          m.ID,
				DisplayName: m.DisplayName,
			})
		}

		if !listResp.HasMore || listResp.LastID == "" {
			break
		}
		afterID = listResp.LastID
	}

	return all, nil
}

func (c *Client) listModelsVertex(ctx context.Context) ([]provider.DiscoveredModel, error) {
	token, err := c.tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("anthropic provider %q: obtaining access token: %w", c.name, err)
	}

	var all []provider.DiscoveredModel
	pageToken := ""

	for {
		u := fmt.Sprintf(
			"https://%s-aiplatform.googleapis.com/v1beta1/publishers/anthropic/models?pageSize=100",
			c.region,
		)
		if pageToken != "" {
			u += "&pageToken=" + pageToken
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("anthropic provider %q: creating vertex list request: %w", c.name, err)
		}
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)

		resp, err := c.httpClient.Do(req) // #nosec G107 -- URL from operator-controlled provider config
		if err != nil {
			return nil, fmt.Errorf("anthropic provider %q: listing vertex models: %w", c.name, err)
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, provider.MaxUpstreamResponseBytes))
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("anthropic provider %q: reading vertex models response: %w", c.name, err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("anthropic provider %q: vertex models endpoint returned %d: %s", c.name, resp.StatusCode, string(body))
		}

		var listResp vertexModelsListResponse
		if err := json.Unmarshal(body, &listResp); err != nil {
			return nil, fmt.Errorf("anthropic provider %q: decoding vertex models response: %w", c.name, err)
		}

		for _, m := range listResp.PublisherModels {
			// Extract model ID from "publishers/anthropic/models/{id}".
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
