// Package pricing computes request costs from token usage and model pricing tables.
package pricing

import "log"

// Entry holds the per-million-token cost for a single model.
type Entry struct {
	InputPerMillion      float64
	OutputPerMillion     float64
	CacheWritePerMillion float64
	CacheReadPerMillion  float64
}

// Calculator computes request costs based on a model pricing table.
type Calculator struct {
	entries map[string]Entry
}

// NewCalculator creates a Calculator from a map of upstream model name to
// Entry.
func NewCalculator(entries map[string]Entry) *Calculator {
	return &Calculator{entries: entries}
}

// Calculate returns the estimated cost for a request given the upstream
// model name and token counts.  If the model is not in the pricing table
// the return value is nil (unknown pricing).
func (c *Calculator) Calculate(upstreamModel string, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64) *float64 {
	entry, ok := c.entries[upstreamModel]
	if !ok {
		return nil
	}

	cost := (float64(inputTokens)*entry.InputPerMillion +
		float64(outputTokens)*entry.OutputPerMillion) / 1_000_000

	if cacheCreationTokens > 0 || cacheReadTokens > 0 {
		if entry.CacheWritePerMillion == 0 && entry.CacheReadPerMillion == 0 {
			log.Printf("WARNING: cache tokens present but no cache pricing configured for model %q", upstreamModel)
		} else {
			cost += (float64(cacheCreationTokens)*entry.CacheWritePerMillion +
				float64(cacheReadTokens)*entry.CacheReadPerMillion) / 1_000_000
		}
	}

	return &cost
}
