// SPDX-License-Identifier: AGPL-3.0-or-later

// Package copilot implements the provider.Provider interface for the GitHub Copilot API.
package copilot

// GitHubToken holds the long-lived GitHub OAuth access token obtained via device flow.
type GitHubToken struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
}

// SessionToken holds the short-lived Copilot API session token
// exchanged from a GitHub OAuth token.
type SessionToken struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	APIBase   string `json:"api_base"`
}

// DeviceFlowResponse holds the response from GitHub's device code endpoint.
type DeviceFlowResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// AccessTokenResponse holds the response from GitHub's OAuth token exchange endpoint.
type AccessTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error,omitempty"`
}

// tokenResponse holds the response from the Copilot internal token endpoint.
type tokenResponse struct {
	Token     string                `json:"token"`
	ExpiresAt int64                 `json:"expires_at"`
	Endpoints tokenEndpointsWrapper `json:"endpoints,omitzero"`
}

// tokenEndpointsWrapper holds the endpoints object from the Copilot token response.
type tokenEndpointsWrapper struct {
	API string `json:"api"`
}

// OpenAI chat completion types used for request/response parsing.

// ChatCompletionResponse is the standard OpenAI chat completion response.
type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   *ChatCompletionUsage   `json:"usage,omitempty"`
}

// ChatCompletionChoice is a single choice in a chat completion response.
type ChatCompletionChoice struct {
	Index        int    `json:"index"`
	FinishReason string `json:"finish_reason,omitempty"`
}

// ChatCompletionUsage holds token usage from an OpenAI response.
type ChatCompletionUsage struct {
	PromptTokens            int64                    `json:"prompt_tokens"`
	CompletionTokens        int64                    `json:"completion_tokens"`
	TotalTokens             int64                    `json:"total_tokens"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}

// PromptTokensDetails holds the prompt token breakdown.
type PromptTokensDetails struct {
	CachedTokens int64 `json:"cached_tokens"`
}

// CompletionTokensDetails holds the completion token breakdown.
type CompletionTokensDetails struct {
	ReasoningTokens int64 `json:"reasoning_tokens"`
}
