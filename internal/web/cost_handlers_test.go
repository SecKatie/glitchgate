// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http/httptest"
	"testing"
	"time"

	"codeberg.org/kglitchy/glitchgate/internal/store"
	"github.com/stretchr/testify/require"
)

func TestParseCostParamsUsesSingleGroupingFilter(t *testing.T) {
	tz := time.FixedZone("EST", -5*60*60)
	h := &CostHandlers{tz: tz}

	t.Run("model mode routes filter to group filter", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/ui/costs?group_by=model&filter=claude&key=llmp_sk_aa11", nil)

		params := h.parseCostParams(req)

		require.Equal(t, "model", params.GroupBy)
		require.Equal(t, "claude", params.GroupFilter)
		require.Empty(t, params.KeyPrefix)
	})

	t.Run("provider mode routes filter to group filter", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/ui/costs?group_by=provider&filter=openai", nil)

		params := h.parseCostParams(req)

		require.Equal(t, "provider", params.GroupBy)
		require.Equal(t, "openai", params.GroupFilter)
		require.Empty(t, params.KeyPrefix)
	})

	t.Run("key mode routes filter to key prefix", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/ui/costs?group_by=key&filter=llmp_sk_aa11", nil)

		params := h.parseCostParams(req)

		require.Equal(t, "key", params.GroupBy)
		require.Equal(t, "llmp_sk_aa11", params.KeyPrefix)
		require.Empty(t, params.GroupFilter)
	})

	t.Run("key mode accepts legacy key query param", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/ui/costs?group_by=key&key=llmp_sk_bb22", nil)

		params := h.parseCostParams(req)

		require.Equal(t, "key", params.GroupBy)
		require.Equal(t, "llmp_sk_bb22", params.KeyPrefix)
		require.Empty(t, params.GroupFilter)
	})
}

func TestParseCostParamsUsesDSTAwareDayBounds(t *testing.T) {
	tz, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	h := &CostHandlers{tz: tz}
	req := httptest.NewRequest("GET", "/ui/costs?from=2026-03-08&to=2026-03-08", nil)

	params := h.parseCostParams(req)

	require.Equal(t, "2026-03-08 05:00:00", params.From)
	require.Equal(t, "2026-03-09 03:59:59", params.To)
	require.Equal(t, -5*60*60, params.TzOffsetSeconds)
	require.Equal(t, tz, params.TzLocation)
}

func TestAggregateProviderBreakdown(t *testing.T) {
	entries := []store.CostBreakdownEntry{
		{
			Group:               "anthropic:api.anthropic.com",
			InputTokens:         100,
			OutputTokens:        50,
			CacheCreationTokens: 5,
			CacheReadTokens:     7,
			Requests:            2,
		},
		{
			Group:               "anthropic:vertex",
			InputTokens:         120,
			OutputTokens:        70,
			CacheCreationTokens: 3,
			CacheReadTokens:     4,
			Requests:            1,
		},
		{
			Group:               "openai",
			InputTokens:         90,
			OutputTokens:        20,
			CacheCreationTokens: 1,
			CacheReadTokens:     2,
			Requests:            3,
		},
	}

	result := aggregateProviderBreakdown(entries, map[string]string{
		"anthropic:api.anthropic.com": "claude-max",
		"anthropic:vertex":            "claude-max",
		"openai":                      "chatgpt-pro",
	})

	require.Len(t, result, 2)
	// Both groups have 3 requests; alphabetical tiebreaker puts "chatgpt-pro" before "claude-max".
	require.Equal(t, "chatgpt-pro", result[0].Group)
	require.Equal(t, int64(3), result[0].Requests)

	require.Equal(t, "claude-max", result[1].Group)
	require.Equal(t, int64(220), result[1].InputTokens)
	require.Equal(t, int64(120), result[1].OutputTokens)
	require.Equal(t, int64(8), result[1].CacheCreationTokens)
	require.Equal(t, int64(11), result[1].CacheReadTokens)
	require.Equal(t, int64(3), result[1].Requests)
}

func TestApplyProviderFilterMatchesDisplayNames(t *testing.T) {
	h := &CostHandlers{
		providerNames: map[string]string{
			"openai":                       "chatgpt-pro",
			"openai_responses:chatgpt.com": "chatgpt-pro",
			"anthropic:api.anthropic.com":  "claude-max",
		},
	}

	params := store.CostParams{
		GroupBy:     "provider",
		GroupFilter: "chatgpt",
	}

	h.applyProviderFilter(&params)

	require.Equal(t, []string{"openai", "openai_responses:chatgpt.com"}, params.ProviderGroups)
}
