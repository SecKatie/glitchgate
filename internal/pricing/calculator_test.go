package pricing_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"codeberg.org/kglitchy/glitchgate/internal/pricing"
)

func TestCalculate(t *testing.T) {
	entries := map[string]pricing.Entry{
		"my-provider/known-model": {
			InputPerMillion:      3.00,
			OutputPerMillion:     15.00,
			CacheWritePerMillion: 3.75,
			CacheReadPerMillion:  0.30,
		},
	}
	calc := pricing.NewCalculator(entries)

	t.Run("unknown model returns nil", func(t *testing.T) {
		got := calc.Calculate("my-provider", "no-such-model", 1000, 500, 0, 0, 0)
		require.Nil(t, got)
	})

	t.Run("input and output tokens only", func(t *testing.T) {
		// 1_000_000 input at $3/M = $3.00, 500_000 output at $15/M = $7.50 → $10.50
		got := calc.Calculate("my-provider", "known-model", 1_000_000, 500_000, 0, 0, 0)
		require.NotNil(t, got)
		require.InDelta(t, 10.50, *got, 1e-9)
	})

	t.Run("cache creation tokens included in cost", func(t *testing.T) {
		// 0 base input, 1_000_000 cache write at $3.75/M = $3.75
		got := calc.Calculate("my-provider", "known-model", 0, 0, 1_000_000, 0, 0)
		require.NotNil(t, got)
		require.InDelta(t, 3.75, *got, 1e-9)
	})

	t.Run("cache read tokens included in cost", func(t *testing.T) {
		// 0 base input, 1_000_000 cache read at $0.30/M = $0.30
		got := calc.Calculate("my-provider", "known-model", 0, 0, 0, 1_000_000, 0)
		require.NotNil(t, got)
		require.InDelta(t, 0.30, *got, 1e-9)
	})

	t.Run("all token types combined", func(t *testing.T) {
		// 1M input $3.00 + 500K output $7.50 + 200K cache write $0.75 + 100K cache read $0.03 = $11.28
		got := calc.Calculate("my-provider", "known-model", 1_000_000, 500_000, 200_000, 100_000, 0)
		require.NotNil(t, got)
		require.InDelta(t, 11.28, *got, 1e-9)
	})

	t.Run("zero tokens returns zero cost", func(t *testing.T) {
		got := calc.Calculate("my-provider", "known-model", 0, 0, 0, 0, 0)
		require.NotNil(t, got)
		require.InDelta(t, 0.0, *got, 1e-9)
	})

	t.Run("reasoning tokens are subset of output not additive", func(t *testing.T) {
		// 1M output at $15/M = $15.00 regardless of reasoning breakdown
		got := calc.Calculate("my-provider", "known-model", 0, 1_000_000, 0, 0, 500_000)
		require.NotNil(t, got)
		require.InDelta(t, 15.00, *got, 1e-9)
	})
}

func TestAnthropicDefaultsHasCacheRates(t *testing.T) {
	for model, entry := range pricing.AnthropicDefaults {
		t.Run(model, func(t *testing.T) {
			require.Greater(t, entry.CacheWritePerMillion, 0.0, "CacheWritePerMillion should be non-zero")
			require.Greater(t, entry.CacheReadPerMillion, 0.0, "CacheReadPerMillion should be non-zero")
		})
	}
}
