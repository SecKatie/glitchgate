// Package pricing computes request costs from token usage and model pricing tables.
package pricing

import "log/slog"

// Entry holds the per-million-token cost and capabilities for a single model.
type Entry struct {
	InputPerMillion       float64
	OutputPerMillion      float64
	CacheWritePerMillion  float64
	CacheReadPerMillion   float64
	ContextWindow         int  // maximum context window in tokens (0 = unknown)
	StandardContextWindow int  // context limit at standard pricing (0 = same as ContextWindow)
	MaxTokens             int  // maximum output tokens (0 = unknown)
	Reasoning             bool // whether the model supports extended thinking/reasoning
	Vision                bool // whether the model supports vision/image input
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
// reasoningTokens is a subset of outputTokens and is NOT additive — it is
// priced at the output rate and included here only for future per-category
// rate support.
func (c *Calculator) Calculate(providerName, upstreamModel string, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, reasoningTokens int64) *float64 { //nolint:revive // reasoningTokens reserved for future per-category rates; currently a subset of outputTokens
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
