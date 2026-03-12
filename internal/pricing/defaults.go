package pricing

import "net/url"

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
