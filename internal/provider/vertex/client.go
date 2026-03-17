// SPDX-License-Identifier: AGPL-3.0-or-later

// Package vertex implements the provider.Provider interface for models
// hosted on Google Cloud Vertex AI.
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
	"github.com/seckatie/glitchgate/internal/provider/anthropic"
)

// Client implements provider.Provider for Claude models on Vertex AI.
type Client struct {
	name           string
	project        string
	region         string
	defaultVersion string
	httpClient     *http.Client
	requestTimeout time.Duration
	tokenSource    oauth2.TokenSource
}

// NewClient creates a Vertex AI provider client for Claude models.
// If credentialsFile is non-empty, it reads that file for service account JSON;
// otherwise it uses Application Default Credentials (ADC).
func NewClient(name, project, region, credentialsFile, defaultVersion string) (*Client, error) {
	ts, err := newTokenSource(name, credentialsFile)
	if err != nil {
		return nil, err
	}

	if defaultVersion == "" {
		defaultVersion = "vertex-2023-10-16"
	}

	return &Client{
		name:           name,
		project:        project,
		region:         region,
		defaultVersion: defaultVersion,
		httpClient:     provider.BuildHTTPClient(),
		requestTimeout: config.DefaultUpstreamRequestTimeout,
		tokenSource:    ts,
	}, nil
}

// newTestClient creates a Client with a custom token source and HTTP client,
// for use in unit tests only.
func newTestClient(name, project, region, defaultVersion string, ts oauth2.TokenSource, httpClient *http.Client) *Client {
	return &Client{
		name:           name,
		project:        project,
		region:         region,
		defaultVersion: defaultVersion,
		httpClient:     httpClient,
		requestTimeout: config.DefaultUpstreamRequestTimeout,
		tokenSource:    ts,
	}
}

// SetTimeouts overrides the default upstream request deadline.
func (c *Client) SetTimeouts(requestTimeout time.Duration) {
	c.requestTimeout = requestTimeout
}

// Name returns the provider's short identifier.
func (c *Client) Name() string { return c.name }

// AuthMode returns "internal" — this provider manages its own OAuth2 auth.
func (c *Client) AuthMode() string { return "internal" }

// APIFormat returns "anthropic" — Vertex Claude speaks the Anthropic Messages API.
func (c *Client) APIFormat() string { return "anthropic" }

// SendRequest dispatches a request to the Vertex AI Claude endpoint.
func (c *Client) SendRequest(ctx context.Context, req *provider.Request) (*provider.Response, error) {
	if !req.IsStreaming {
		var cancel context.CancelFunc
		ctx, cancel = provider.ContextWithDefaultTimeout(ctx, c.requestTimeout)
		defer cancel()
	}

	token, err := c.tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("vertex provider %q: obtaining access token: %w", c.name, err)
	}

	body, err := prepareBody(req.Body, c.defaultVersion)
	if err != nil {
		return nil, fmt.Errorf("vertex provider %q: preparing request body: %w", c.name, err)
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

	resp, err := c.httpClient.Do(httpReq) // #nosec G704 -- URL is constructed from operator-controlled config (project, region) and model dispatch
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
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, provider.MaxUpstreamResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", c.name, err)
	}
	provResp.Body = respBody

	if resp.StatusCode == http.StatusOK {
		var msgResp anthropic.MessagesResponse
		if err := json.Unmarshal(respBody, &msgResp); err == nil {
			provResp.InputTokens = msgResp.Usage.InputTokens
			provResp.OutputTokens = msgResp.Usage.OutputTokens
			provResp.CacheCreationInputTokens = msgResp.Usage.CacheCreationInputTokens
			provResp.CacheReadInputTokens = msgResp.Usage.CacheReadInputTokens
		}
	}

	return provResp, nil
}

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
