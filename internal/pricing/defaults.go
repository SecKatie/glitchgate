package pricing

// DefaultPricing contains pricing data for well-known Anthropic models.
// Values are USD per million tokens as of 2025-05-14.
// Cache write is billed at 1.25× the base input rate.
// Cache read is billed at 0.10× the base input rate.
var DefaultPricing = map[string]Entry{
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
