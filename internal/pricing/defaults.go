package pricing

// DefaultPricing contains pricing data for well-known models.
// Values are USD per million tokens as of 2026-03-11.
//
// Cache semantics vary by provider:
//   - Anthropic: CacheWrite is 1.25× input (5-min TTL); CacheRead is 0.10× input.
//   - OpenAI: no explicit cache-write cost (automatic); CacheRead is the discounted cached-input price.
//   - Google: no per-token write cost (storage billed hourly, not captured here); CacheRead is ~10% of input.
//
// For OpenAI models with tiered pricing (e.g. gpt-5.4), standard (<272K context) rates are used.
// For Gemini models with tiered pricing (e.g. gemini-3.1-pro-preview), standard (≤200K context) rates are used.
var DefaultPricing = map[string]Entry{
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

	// -------------------------------------------------------------------------
	// OpenAI GPT-5 (latest) — standard (<272K context) rates
	// -------------------------------------------------------------------------
	"gpt-5.4": {
		InputPerMillion:      2.50,
		OutputPerMillion:     15.00,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0.25,
	},
	"gpt-5.4-pro": {
		InputPerMillion:      30.00,
		OutputPerMillion:     180.00,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0,
	},
	"gpt-5.2": {
		InputPerMillion:      1.75,
		OutputPerMillion:     14.00,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0.175,
	},
	"gpt-5.2-pro": {
		InputPerMillion:      21.00,
		OutputPerMillion:     168.00,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0,
	},
	"gpt-5.1": {
		InputPerMillion:      1.25,
		OutputPerMillion:     10.00,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0.125,
	},
	"gpt-5": {
		InputPerMillion:      1.25,
		OutputPerMillion:     10.00,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0.125,
	},
	"gpt-5-pro": {
		InputPerMillion:      15.00,
		OutputPerMillion:     120.00,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0,
	},
	"gpt-5-mini": {
		InputPerMillion:      0.25,
		OutputPerMillion:     2.00,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0.025,
	},
	"gpt-5-nano": {
		InputPerMillion:      0.05,
		OutputPerMillion:     0.40,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0.005,
	},

	// -------------------------------------------------------------------------
	// OpenAI GPT-4 (previous generation)
	// -------------------------------------------------------------------------
	"gpt-4.1": {
		InputPerMillion:      2.00,
		OutputPerMillion:     8.00,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0.50,
	},
	"gpt-4.1-mini": {
		InputPerMillion:      0.40,
		OutputPerMillion:     1.60,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0.10,
	},
	"gpt-4.1-nano": {
		InputPerMillion:      0.10,
		OutputPerMillion:     0.40,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0.025,
	},
	"gpt-4o": {
		InputPerMillion:      2.50,
		OutputPerMillion:     10.00,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  1.25,
	},
	"gpt-4o-mini": {
		InputPerMillion:      0.15,
		OutputPerMillion:     0.60,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0.075,
	},

	// -------------------------------------------------------------------------
	// OpenAI Reasoning models
	// -------------------------------------------------------------------------
	"o4-mini": {
		InputPerMillion:      1.10,
		OutputPerMillion:     4.40,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0.275,
	},
	"o3": {
		InputPerMillion:      2.00,
		OutputPerMillion:     8.00,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0.50,
	},
	"o3-pro": {
		InputPerMillion:      20.00,
		OutputPerMillion:     80.00,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0,
	},
	"o3-mini": {
		InputPerMillion:      1.10,
		OutputPerMillion:     4.40,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0.55,
	},
	"o1": {
		InputPerMillion:      15.00,
		OutputPerMillion:     60.00,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  7.50,
	},

	// -------------------------------------------------------------------------
	// Google Gemini 3.x — standard rates
	// No per-token cache-write cost; storage is billed hourly (not captured here).
	// -------------------------------------------------------------------------
	"gemini-3.1-pro-preview": {
		InputPerMillion:      2.00,
		OutputPerMillion:     12.00,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0.20,
	},
	"gemini-3.1-flash-lite-preview": {
		InputPerMillion:      0.25,
		OutputPerMillion:     1.50,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0.025,
	},
	"gemini-3-flash-preview": {
		InputPerMillion:      0.50,
		OutputPerMillion:     3.00,
		CacheWritePerMillion: 0,
		CacheReadPerMillion:  0.05,
	},
}
