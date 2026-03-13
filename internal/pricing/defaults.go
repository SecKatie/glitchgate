package pricing

import (
	"net/url"
	"strings"
)

// AnthropicDefaults holds pricing for Claude models billed at official Anthropic API rates.
// Applied only for providers with type "anthropic" whose base_url matches the official endpoint.
// Values are USD per million tokens as of 2026-03-11.
//
// Cache semantics: CacheWrite is 1.25× input (5-min TTL); CacheRead is 0.10× input.
var AnthropicDefaults = map[string]Entry{
	// Claude 4.6 (latest)
	"claude-opus-4-6": {
		InputPerMillion:      5.00,
		OutputPerMillion:     25.00,
		CacheWritePerMillion: 6.25,
		CacheReadPerMillion:  0.50,
	},
	"claude-sonnet-4-6": {
		InputPerMillion:      3.00,
		OutputPerMillion:     15.00,
		CacheWritePerMillion: 3.75,
		CacheReadPerMillion:  0.30,
	},
	// Claude Haiku 4.5 (latest — dated ID + alias)
	"claude-haiku-4-5-20251001": {
		InputPerMillion:      1.00,
		OutputPerMillion:     5.00,
		CacheWritePerMillion: 1.25,
		CacheReadPerMillion:  0.10,
	},
	"claude-haiku-4-5": {
		InputPerMillion:      1.00,
		OutputPerMillion:     5.00,
		CacheWritePerMillion: 1.25,
		CacheReadPerMillion:  0.10,
	},
	// Claude 4 (legacy)
	"claude-sonnet-4-20250514": {
		InputPerMillion:      3.00,
		OutputPerMillion:     15.00,
		CacheWritePerMillion: 3.75,
		CacheReadPerMillion:  0.30,
	},
	"claude-opus-4-20250514": {
		InputPerMillion:      15.00,
		OutputPerMillion:     75.00,
		CacheWritePerMillion: 18.75,
		CacheReadPerMillion:  1.50,
	},
	"claude-haiku-4-20250514": {
		InputPerMillion:      0.80,
		OutputPerMillion:     4.00,
		CacheWritePerMillion: 1.00,
		CacheReadPerMillion:  0.08,
	},
}

// CopilotDefaults holds $0 pricing for GitHub Copilot models.
// Applied for all providers with type "github_copilot".
// Copilot is subscription-billed; entries exist so requests are tracked without inflating costs.
var CopilotDefaults = map[string]Entry{
	"claude-opus-4.6":   {InputPerMillion: 0, OutputPerMillion: 0},
	"claude-sonnet-4.6": {InputPerMillion: 0, OutputPerMillion: 0},
	"gpt-5.2":           {InputPerMillion: 0, OutputPerMillion: 0},
	"gpt-5.4":           {InputPerMillion: 0, OutputPerMillion: 0},
	"gpt-5.4-pro":       {InputPerMillion: 0, OutputPerMillion: 0},
	"o4-mini":           {InputPerMillion: 0, OutputPerMillion: 0},
	"o3":                {InputPerMillion: 0, OutputPerMillion: 0},
	"gemini-3-flash":    {InputPerMillion: 0, OutputPerMillion: 0},
}

// OpenAIDefaults holds pricing for OpenAI models billed at official API-equivalent rates.
// Applied only for providers with type "openai" or "openai_responses" whose
// base_url matches the official API endpoint or the ChatGPT Codex backend.
// Values are USD per million tokens as of 2026-03-13.
var OpenAIDefaults = map[string]Entry{
	"gpt-4o": {
		InputPerMillion:     2.50,
		OutputPerMillion:    10.00,
		CacheReadPerMillion: 1.25,
	},
	"gpt-4o-mini": {
		InputPerMillion:     0.15,
		OutputPerMillion:    0.60,
		CacheReadPerMillion: 0.075,
	},
	"gpt-4.1": {
		InputPerMillion:     2.00,
		OutputPerMillion:    8.00,
		CacheReadPerMillion: 0.50,
	},
	"gpt-4.1-mini": {
		InputPerMillion:     0.40,
		OutputPerMillion:    1.60,
		CacheReadPerMillion: 0.10,
	},
	"gpt-4.1-nano": {
		InputPerMillion:     0.10,
		OutputPerMillion:    0.40,
		CacheReadPerMillion: 0.025,
	},
	"gpt-5.4": {
		InputPerMillion:     2.50,
		OutputPerMillion:    15.00,
		CacheReadPerMillion: 0.25,
	},
	"o3": {
		InputPerMillion:  2.00,
		OutputPerMillion: 8.00,
	},
	"o3-mini": {
		InputPerMillion:  1.10,
		OutputPerMillion: 4.40,
	},
	"o4-mini": {
		InputPerMillion:  1.10,
		OutputPerMillion: 4.40,
	},
}

const (
	officialOpenAIHost      = "api.openai.com"
	officialChatGPTHost     = "chatgpt.com"
	officialCodexPathPrefix = "/backend-api/codex"
)

// IsOfficialOpenAIURL reports whether baseURL targets an official OpenAI-priced endpoint.
// This includes the public API host and the ChatGPT Codex backend path.
func IsOfficialOpenAIURL(baseURL string) bool {
	if baseURL == "" {
		return false
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}

	switch parsed.Hostname() {
	case officialOpenAIHost:
		return true
	case officialChatGPTHost:
		return strings.HasPrefix(strings.TrimRight(parsed.Path, "/"), officialCodexPathPrefix)
	default:
		return false
	}
}

const officialAnthropicHost = "api.anthropic.com"

// IsOfficialAnthropicURL reports whether baseURL targets the official Anthropic API.
func IsOfficialAnthropicURL(baseURL string) bool {
	return hostnameOf(baseURL) == officialAnthropicHost
}

// ProviderKey returns the canonical provider identifier used as the key prefix
// in the pricing calculator: "{type}:{hostname}".
// baseURL should be the provider's configured base URL (or the canonical default
// for providers whose URL is not user-configurable, such as GitHub Copilot).
func ProviderKey(providerType, baseURL string) string {
	host := hostnameOf(baseURL)
	if host == "" {
		return providerType
	}
	return providerType + ":" + host
}

// hostnameOf parses u and returns its hostname, or "" on error or empty input.
func hostnameOf(u string) string {
	if u == "" {
		return ""
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
