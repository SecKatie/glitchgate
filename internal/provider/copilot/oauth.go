// SPDX-License-Identifier: AGPL-3.0-or-later

package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// GitHub OAuth client ID used by Copilot proxy implementations.
	oauthClientID = "Iv1.b507a08c87ecfe98"
	oauthScope    = "read:user"

	deviceCodeURL     = "https://github.com/login/device/code"
	accessTokenURL    = "https://github.com/login/oauth/access_token"      // #nosec G101 -- URL, not a credential
	copilotTokenURL   = "https://api.github.com/copilot_internal/v2/token" // #nosec G101 -- URL, not a credential
	defaultCopilotAPI = "https://api.githubcopilot.com"

	editorVersion        = "vscode/1.85.1"
	editorPluginVersion  = "copilot/1.155.0"
	copilotIntegrationID = "vscode-chat"
	copilotUserAgent     = "GithubCopilot/1.155.0"
)

// RequestDeviceCode initiates the GitHub OAuth device flow and returns the
// device code, user code, and verification URI for the operator to authorize.
func RequestDeviceCode(ctx context.Context) (*DeviceFlowResponse, error) {
	form := url.Values{
		"client_id": {oauthClientID},
		"scope":     {oauthScope},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deviceCodeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating device code request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting device code: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("device code request failed (%d): %s", resp.StatusCode, string(body))
	}

	var result DeviceFlowResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding device code response: %w", err)
	}
	return &result, nil
}

// PollForAccessToken polls GitHub's OAuth token endpoint until the user
// authorizes the device or the device code expires.
func PollForAccessToken(ctx context.Context, deviceCode string, interval int, expiresIn int) (*GitHubToken, error) {
	if interval < 1 {
		interval = 5
	}
	if expiresIn < 1 {
		expiresIn = 900
	}

	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)
	currentInterval := time.Duration(interval) * time.Second

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(currentInterval):
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("device code expired — please re-run the auth command")
			}

			token, slowDown, err := exchangeDeviceCode(ctx, deviceCode)
			if err != nil {
				return nil, err
			}
			if slowDown {
				// GitHub requires backing off; increase interval by 5s per spec.
				currentInterval += 5 * time.Second
				continue
			}
			if token != nil {
				return token, nil
			}
			// token == nil means authorization_pending, continue polling
		}
	}
}

func exchangeDeviceCode(ctx context.Context, deviceCode string) (token *GitHubToken, slowDown bool, err error) {
	form := url.Values{
		"client_id":   {oauthClientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, accessTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, false, fmt.Errorf("creating token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("exchanging device code: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result AccessTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, false, fmt.Errorf("decoding token response: %w", err)
	}

	switch result.Error {
	case "":
		// Success
		return &GitHubToken{
			AccessToken: result.AccessToken,
			TokenType:   result.TokenType,
			Scope:       result.Scope,
		}, false, nil
	case "authorization_pending":
		return nil, false, nil // Keep polling
	case "slow_down":
		return nil, true, nil // Signal caller to increase interval per RFC 8628
	case "expired_token":
		return nil, false, fmt.Errorf("device code expired — please re-run the auth command")
	case "access_denied":
		return nil, false, fmt.Errorf("authorization denied by user")
	default:
		return nil, false, fmt.Errorf("OAuth error: %s", result.Error)
	}
}

// ExchangeForCopilotToken exchanges a GitHub OAuth token for a short-lived
// Copilot API session token.
func ExchangeForCopilotToken(ctx context.Context, githubToken string) (*SessionToken, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, copilotTokenURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating copilot token request: %w", err)
	}
	req.Header.Set("Authorization", "token "+githubToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Editor-Version", editorVersion)
	req.Header.Set("Editor-Plugin-Version", editorPluginVersion)
	req.Header.Set("User-Agent", copilotUserAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exchanging for copilot token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("GitHub token is invalid or revoked — please re-run: llm-proxy auth copilot")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("copilot token exchange failed (%d): %s", resp.StatusCode, string(body))
	}

	var result tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding copilot token response: %w", err)
	}

	apiBase := result.Endpoints.API
	if apiBase == "" {
		apiBase = defaultCopilotAPI
	}

	return &SessionToken{
		Token:     result.Token,
		ExpiresAt: result.ExpiresAt,
		APIBase:   apiBase,
	}, nil
}
