// Package pricing computes request costs from token usage and model pricing tables.
package pricing

import "log/slog"

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

// NewCalculator creates a Calculator from a map of "providerName/upstreamModel" to Entry.
func NewCalculator(entries map[string]Entry) *Calculator {
	return &Calculator{entries: entries}
}

// Lookup returns the pricing Entry for the given provider and upstream model.
// The second return value is false when the combination is not in the pricing table.
func (c *Calculator) Lookup(providerName, upstreamModel string) (Entry, bool) {
	entry, ok := c.entries[providerName+"/"+upstreamModel]
	return entry, ok
}

// Calculate returns the estimated cost for a request given the provider name,
// upstream model name, and token counts. If the model is not in the pricing
// table the return value is nil (unknown pricing).
func (c *Calculator) Calculate(providerName, upstreamModel string, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64) *float64 {
	entry, ok := c.entries[providerName+"/"+upstreamModel]
	if !ok {
		return nil
	}

	cost := (float64(inputTokens)*entry.InputPerMillion +
		float64(outputTokens)*entry.OutputPerMillion) / 1_000_000

	if cacheCreationTokens > 0 || cacheReadTokens > 0 {
		if entry.CacheWritePerMillion == 0 && entry.CacheReadPerMillion == 0 {
			slog.Warn("cache tokens present but no cache pricing configured", "model", upstreamModel)
		} else {
			cost += (float64(cacheCreationTokens)*entry.CacheWritePerMillion +
				float64(cacheReadTokens)*entry.CacheReadPerMillion) / 1_000_000
		}
	}

	return &cost
}
