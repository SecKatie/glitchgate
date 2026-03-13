// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"codeberg.org/kglitchy/glitchgate/internal/pricing"
	"codeberg.org/kglitchy/glitchgate/internal/store"
	"github.com/stretchr/testify/require"
)

type stubCostAPIStore struct {
	store.CostQueryStore
	summary               *store.CostSummary
	breakdown             []store.CostBreakdownEntry
	pricingGroups         []store.CostPricingGroup
	timeseriesPricingData []store.CostTimeseriesPricingGroup
}

func (s *stubCostAPIStore) GetCostSummary(context.Context, store.CostParams) (*store.CostSummary, error) {
	return s.summary, nil
}

func (s *stubCostAPIStore) GetCostBreakdown(context.Context, store.CostParams) ([]store.CostBreakdownEntry, error) {
	return s.breakdown, nil
}

func (s *stubCostAPIStore) GetCostPricingGroups(context.Context, store.CostParams) ([]store.CostPricingGroup, error) {
	return s.pricingGroups, nil
}

func (s *stubCostAPIStore) GetCostTimeseriesPricingGroups(context.Context, store.CostParams) ([]store.CostTimeseriesPricingGroup, error) {
	return s.timeseriesPricingData, nil
}

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
			Group:               "claude-max",
			InputTokens:         100,
			OutputTokens:        50,
			CacheCreationTokens: 5,
			CacheReadTokens:     7,
			Requests:            2,
		},
		{
			Group:               "chatgpt-pro",
			InputTokens:         120,
			OutputTokens:        70,
			CacheCreationTokens: 3,
			CacheReadTokens:     4,
			Requests:            1,
		},
		{
			Group:               "copilot",
			InputTokens:         90,
			OutputTokens:        20,
			CacheCreationTokens: 1,
			CacheReadTokens:     2,
			Requests:            3,
		},
	}

	result := aggregateProviderBreakdown(entries, map[string]string{
		"claude-max":  "claude-max",
		"chatgpt-pro": "chatgpt-pro",
		"copilot":     "copilot",
	})

	require.Len(t, result, 3)
	require.Equal(t, "copilot", result[0].Group)
	require.Equal(t, int64(3), result[0].Requests)

	require.Equal(t, "claude-max", result[1].Group)
	require.Equal(t, int64(100), result[1].InputTokens)
	require.Equal(t, int64(50), result[1].OutputTokens)
	require.Equal(t, int64(2), result[1].Requests)

	require.Equal(t, "chatgpt-pro", result[2].Group)
	require.Equal(t, int64(120), result[2].InputTokens)
	require.Equal(t, int64(70), result[2].OutputTokens)
	require.Equal(t, int64(1), result[2].Requests)
}

func TestAggregateProviderBreakdownFallsBackToStoredProviderName(t *testing.T) {
	entries := []store.CostBreakdownEntry{
		{
			Group:               "claude-max",
			InputTokens:         220,
			OutputTokens:        120,
			CacheCreationTokens: 8,
			CacheReadTokens:     11,
			Requests:            3,
		},
	}

	result := aggregateProviderBreakdown(entries, nil)

	require.Len(t, result, 1)
	require.Equal(t, "claude-max", result[0].Group)
	require.Equal(t, int64(220), result[0].InputTokens)
	require.Equal(t, int64(120), result[0].OutputTokens)
	require.Equal(t, int64(8), result[0].CacheCreationTokens)
	require.Equal(t, int64(11), result[0].CacheReadTokens)
	require.Equal(t, int64(3), result[0].Requests)
}

func TestApplyProviderFilterMatchesDisplayNames(t *testing.T) {
	h := &CostHandlers{
		providerNames: map[string]string{
			"chatgpt-pro":                  "chatgpt-pro",
			"openai_responses:chatgpt.com": "chatgpt-pro",
			"claude-max":                   "claude-max",
		},
	}

	params := store.CostParams{
		GroupBy:     "provider",
		GroupFilter: "chatgpt",
	}

	h.applyProviderFilter(&params)

	require.Equal(t, []string{"chatgpt-pro", "openai_responses:chatgpt.com"}, params.ProviderGroups)
}

func TestBuildProviderSpendComparisons(t *testing.T) {
	breakdown := []store.CostBreakdownEntry{
		{Group: "chatgpt-pro", InputTokens: 100, OutputTokens: 20, Requests: 2},
		{Group: "claude-max", Requests: 1},
	}
	breakdownCosts := map[string]float64{
		"chatgpt-pro": 18.50,
		"claude-max":  42.00,
	}
	subscriptions := map[string]float64{
		"chatgpt-pro": 20.00,
	}

	comparisons, summary := buildProviderSpendComparisons(breakdown, breakdownCosts, subscriptions)

	require.Len(t, comparisons, 1)
	require.NotNil(t, comparisons["chatgpt-pro"])
	require.Equal(t, 20.0, comparisons["chatgpt-pro"].MonthlySubscriptionCost)
	require.InDelta(t, -1.5, comparisons["chatgpt-pro"].TokenMinusSubscriptionUSD, 1e-9)
	require.NotNil(t, comparisons["chatgpt-pro"].TokenVsSubscriptionPct)
	require.InDelta(t, -7.5, *comparisons["chatgpt-pro"].TokenVsSubscriptionPct, 1e-9)
	require.Equal(t, int64(120), comparisons["chatgpt-pro"].TotalTokens)
	require.NotNil(t, comparisons["chatgpt-pro"].EffectiveTokenCostPerMTok)
	require.InDelta(t, 166666.6666666667, *comparisons["chatgpt-pro"].EffectiveTokenCostPerMTok, 1e-6)
	require.NotNil(t, comparisons["chatgpt-pro"].AverageRealTokenCostPerMTok)
	require.InDelta(t, 154166.6666666667, *comparisons["chatgpt-pro"].AverageRealTokenCostPerMTok, 1e-6)
	require.NotNil(t, comparisons["chatgpt-pro"].EffectiveMinusRealCostPerMTok)
	require.InDelta(t, 12500.0, *comparisons["chatgpt-pro"].EffectiveMinusRealCostPerMTok, 1e-6)
	require.NotNil(t, comparisons["chatgpt-pro"].EffectiveVsRealPct)
	require.InDelta(t, 8.108108108108109, *comparisons["chatgpt-pro"].EffectiveVsRealPct, 1e-9)
	require.True(t, summary.HasAnySubscription)
	require.Equal(t, 1, summary.ComparedProviders)
	require.InDelta(t, 18.5, summary.TotalTokenCostUSD, 1e-9)
	require.InDelta(t, 20.0, summary.TotalSubscriptionCost, 1e-9)
	require.InDelta(t, -1.5, summary.TokenMinusSubscription, 1e-9)
	require.NotNil(t, summary.TokenVsSubscriptionPct)
	require.InDelta(t, -7.5, *summary.TokenVsSubscriptionPct, 1e-9)
	require.Equal(t, int64(120), summary.TotalTokens)
	require.NotNil(t, summary.EffectiveTokenCostPerMTok)
	require.InDelta(t, 166666.6666666667, *summary.EffectiveTokenCostPerMTok, 1e-6)
	require.NotNil(t, summary.AverageRealTokenCostPerMTok)
	require.InDelta(t, 154166.6666666667, *summary.AverageRealTokenCostPerMTok, 1e-6)
	require.NotNil(t, summary.EffectiveMinusRealCostPerMTok)
	require.InDelta(t, 12500.0, *summary.EffectiveMinusRealCostPerMTok, 1e-6)
	require.NotNil(t, summary.EffectiveVsRealPct)
	require.InDelta(t, 8.108108108108109, *summary.EffectiveVsRealPct, 1e-9)
}

func TestCostSummaryHandlerIncludesComputedDollars(t *testing.T) {
	calc := pricing.NewCalculator(map[string]pricing.Entry{
		"anthropic/claude-sonnet-4-20250514": {
			InputPerMillion:  3,
			OutputPerMillion: 15,
		},
	})
	h := &CostHandlers{
		store: &stubCostAPIStore{
			summary: &store.CostSummary{
				TotalInputTokens:  100,
				TotalOutputTokens: 50,
				TotalRequests:     2,
			},
			breakdown: []store.CostBreakdownEntry{
				{
					Group:        "claude-sonnet",
					InputTokens:  100,
					OutputTokens: 50,
					Requests:     2,
				},
			},
			pricingGroups: []store.CostPricingGroup{
				{
					ModelRequested: "claude-sonnet",
					ProviderName:   "anthropic",
					ModelUpstream:  "claude-sonnet-4-20250514",
					InputTokens:    100,
					OutputTokens:   50,
				},
			},
		},
		tz:   time.UTC,
		calc: calc,
	}

	req := httptest.NewRequest("GET", "/ui/api/costs?from=2026-03-01&to=2026-03-31&group_by=model", nil)
	rec := httptest.NewRecorder()

	h.CostSummaryHandler(rec, req)

	require.Equal(t, 200, rec.Code)
	var resp costSummaryResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.InDelta(t, 0.00105, resp.TotalCostUSD, 1e-9)
	require.Len(t, resp.Breakdown, 1)
	require.Equal(t, "claude-sonnet", resp.Breakdown[0].Group)
	require.InDelta(t, 0.00105, resp.Breakdown[0].CostUSD, 1e-9)
}

func TestCostSummaryHandlerAggregatesProviderDisplayCosts(t *testing.T) {
	calc := pricing.NewCalculator(map[string]pricing.Entry{
		"claude-max/claude-sonnet-4-20250514": {
			InputPerMillion:  3,
			OutputPerMillion: 15,
		},
		"claude-max/claude-opus-4-20250514": {
			InputPerMillion:  15,
			OutputPerMillion: 75,
		},
	})
	h := &CostHandlers{
		store: &stubCostAPIStore{
			summary: &store.CostSummary{TotalRequests: 2},
			breakdown: []store.CostBreakdownEntry{
				{Group: "claude-max", InputTokens: 120, OutputTokens: 60, Requests: 2},
			},
			pricingGroups: []store.CostPricingGroup{
				{ProviderName: "claude-max", ModelUpstream: "claude-sonnet-4-20250514", InputTokens: 100, OutputTokens: 50},
				{ProviderName: "claude-max", ModelUpstream: "claude-opus-4-20250514", InputTokens: 20, OutputTokens: 10},
			},
		},
		tz:   time.UTC,
		calc: calc,
		providerNames: map[string]string{
			"claude-max":                  "claude-max",
			"anthropic:api.anthropic.com": "claude-max",
		},
	}

	req := httptest.NewRequest("GET", "/ui/api/costs?from=2026-03-01&to=2026-03-31&group_by=provider", nil)
	rec := httptest.NewRecorder()

	h.CostSummaryHandler(rec, req)

	require.Equal(t, 200, rec.Code)
	var resp costSummaryResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(t, resp.Breakdown, 1)
	require.Equal(t, "claude-max", resp.Breakdown[0].Group)
	require.InDelta(t, 0.00210, resp.Breakdown[0].CostUSD, 1e-9)
	require.InDelta(t, 0.00210, resp.TotalCostUSD, 1e-9)
}

func TestCostSummaryHandlerAggregatesLegacyProviderDisplayCosts(t *testing.T) {
	calc := pricing.NewCalculator(map[string]pricing.Entry{
		"chatgpt-pro/gpt-5.4": {
			InputPerMillion:  2.5,
			OutputPerMillion: 15,
		},
	})
	h := &CostHandlers{
		store: &stubCostAPIStore{
			summary: &store.CostSummary{TotalRequests: 2},
			breakdown: []store.CostBreakdownEntry{
				{Group: "chatgpt-pro", InputTokens: 100, OutputTokens: 20, Requests: 1},
				{Group: "openai_responses:chatgpt.com", InputTokens: 200, OutputTokens: 40, Requests: 1},
			},
			pricingGroups: []store.CostPricingGroup{
				{ProviderName: "chatgpt-pro", ModelUpstream: "gpt-5.4", InputTokens: 100, OutputTokens: 20},
				{ProviderName: "openai_responses:chatgpt.com", ModelUpstream: "gpt-5.4", InputTokens: 200, OutputTokens: 40},
			},
		},
		tz:   time.UTC,
		calc: calc,
		providerNames: map[string]string{
			"chatgpt-pro":                  "chatgpt-pro",
			"openai_responses:chatgpt.com": "chatgpt-pro",
		},
	}

	req := httptest.NewRequest("GET", "/ui/api/costs?from=2026-03-01&to=2026-03-31&group_by=provider", nil)
	rec := httptest.NewRecorder()

	h.CostSummaryHandler(rec, req)

	require.Equal(t, 200, rec.Code)
	var resp costSummaryResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(t, resp.Breakdown, 1)
	require.Equal(t, "chatgpt-pro", resp.Breakdown[0].Group)
	require.InDelta(t, 0.00165, resp.Breakdown[0].CostUSD, 1e-9)
	require.InDelta(t, 0.00165, resp.TotalCostUSD, 1e-9)
}

func TestCostSummaryHandlerIncludesProviderSubscriptionComparisons(t *testing.T) {
	calc := pricing.NewCalculator(map[string]pricing.Entry{
		"chatgpt-pro/gpt-5.4": {
			InputPerMillion:  2.5,
			OutputPerMillion: 15,
		},
	})
	h := &CostHandlers{
		store: &stubCostAPIStore{
			summary: &store.CostSummary{TotalRequests: 1},
			breakdown: []store.CostBreakdownEntry{
				{Group: "chatgpt-pro", InputTokens: 100, OutputTokens: 20, Requests: 1},
			},
			pricingGroups: []store.CostPricingGroup{
				{ProviderName: "chatgpt-pro", ModelUpstream: "gpt-5.4", InputTokens: 100, OutputTokens: 20},
			},
		},
		tz:   time.UTC,
		calc: calc,
		providerMonthlySubscriptions: map[string]float64{
			"chatgpt-pro": 20.0,
		},
	}

	req := httptest.NewRequest("GET", "/ui/api/costs?from=2026-03-01&to=2026-03-31&group_by=provider", nil)
	rec := httptest.NewRecorder()

	h.CostSummaryHandler(rec, req)

	require.Equal(t, 200, rec.Code)
	var resp costSummaryResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.NotNil(t, resp.TotalMonthlySubscriptionCostUSD)
	require.Equal(t, 20.0, *resp.TotalMonthlySubscriptionCostUSD)
	require.NotNil(t, resp.TotalTokenMinusSubscriptionUSD)
	require.InDelta(t, -19.99945, *resp.TotalTokenMinusSubscriptionUSD, 1e-9)
	require.NotNil(t, resp.TotalTokenVsSubscriptionPct)
	require.InDelta(t, -99.99725, *resp.TotalTokenVsSubscriptionPct, 1e-9)
	require.NotNil(t, resp.EffectiveTokenCostPerMTokUSD)
	require.InDelta(t, 166666.6666666667, *resp.EffectiveTokenCostPerMTokUSD, 1e-6)
	require.NotNil(t, resp.AverageRealTokenCostPerMTokUSD)
	require.InDelta(t, 4.583333333333333, *resp.AverageRealTokenCostPerMTokUSD, 1e-6)
	require.NotNil(t, resp.EffectiveMinusRealCostPerMTokUSD)
	require.InDelta(t, 166662.08333333334, *resp.EffectiveMinusRealCostPerMTokUSD, 1e-6)
	require.NotNil(t, resp.EffectiveVsRealPct)
	require.InDelta(t, 3636263.6363636362, *resp.EffectiveVsRealPct, 1e-3)
	require.Len(t, resp.Breakdown, 1)
	require.NotNil(t, resp.Breakdown[0].MonthlySubscriptionCostUSD)
	require.Equal(t, 20.0, *resp.Breakdown[0].MonthlySubscriptionCostUSD)
	require.NotNil(t, resp.Breakdown[0].TokenMinusSubscriptionUSD)
	require.InDelta(t, -19.99945, *resp.Breakdown[0].TokenMinusSubscriptionUSD, 1e-9)
	require.NotNil(t, resp.Breakdown[0].TokenVsSubscriptionPct)
	require.InDelta(t, -99.99725, *resp.Breakdown[0].TokenVsSubscriptionPct, 1e-9)
	require.NotNil(t, resp.Breakdown[0].EffectiveTokenCostPerMTok)
	require.InDelta(t, 166666.6666666667, *resp.Breakdown[0].EffectiveTokenCostPerMTok, 1e-6)
	require.NotNil(t, resp.Breakdown[0].AverageRealTokenCostPerMTok)
	require.InDelta(t, 4.583333333333333, *resp.Breakdown[0].AverageRealTokenCostPerMTok, 1e-6)
	require.NotNil(t, resp.Breakdown[0].EffectiveMinusRealCostPerMTok)
	require.InDelta(t, 166662.08333333334, *resp.Breakdown[0].EffectiveMinusRealCostPerMTok, 1e-6)
	require.NotNil(t, resp.Breakdown[0].EffectiveVsRealPct)
	require.InDelta(t, 3636263.6363636362, *resp.Breakdown[0].EffectiveVsRealPct, 1e-3)
}

func TestCostSummaryHandlerIncludesKeyBreakdownCosts(t *testing.T) {
	calc := pricing.NewCalculator(map[string]pricing.Entry{
		"claude-max/claude-sonnet-4-6": {
			InputPerMillion:  3,
			OutputPerMillion: 15,
		},
	})
	h := &CostHandlers{
		store: &stubCostAPIStore{
			summary: &store.CostSummary{
				TotalInputTokens:  100,
				TotalOutputTokens: 50,
				TotalRequests:     2,
			},
			breakdown: []store.CostBreakdownEntry{
				{
					Group:        "llmp_sk_aa11 (key-alpha)",
					InputTokens:  100,
					OutputTokens: 50,
					Requests:     2,
				},
			},
			pricingGroups: []store.CostPricingGroup{
				{
					ModelRequested: "cm/claude-sonnet-4-6",
					ProviderName:   "claude-max",
					ModelUpstream:  "claude-sonnet-4-6",
					ProxyKeyPrefix: "llmp_sk_aa11",
					ProxyKeyGroup:  "llmp_sk_aa11 (key-alpha)",
					InputTokens:    100,
					OutputTokens:   50,
				},
			},
		},
		tz:   time.UTC,
		calc: calc,
	}

	req := httptest.NewRequest("GET", "/ui/api/costs?from=2026-03-01&to=2026-03-31&group_by=key", nil)
	rec := httptest.NewRecorder()

	h.CostSummaryHandler(rec, req)

	require.Equal(t, 200, rec.Code)
	var resp costSummaryResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(t, resp.Breakdown, 1)
	require.Equal(t, "llmp_sk_aa11 (key-alpha)", resp.Breakdown[0].Group)
	require.InDelta(t, 0.00105, resp.Breakdown[0].CostUSD, 1e-9)
}

func TestCostTimeseriesHandlerIncludesComputedDollars(t *testing.T) {
	calc := pricing.NewCalculator(map[string]pricing.Entry{
		"anthropic/claude-sonnet-4-20250514": {
			InputPerMillion:  3,
			OutputPerMillion: 15,
		},
		"openai/gpt-4o": {
			InputPerMillion:  2.5,
			OutputPerMillion: 10,
		},
	})
	h := &CostHandlers{
		store: &stubCostAPIStore{
			timeseriesPricingData: []store.CostTimeseriesPricingGroup{
				{Date: "2026-03-03", ProviderName: "anthropic", ModelUpstream: "claude-sonnet-4-20250514", InputTokens: 100, OutputTokens: 50, Requests: 1},
				{Date: "2026-03-04", ProviderName: "openai", ModelUpstream: "gpt-4o", InputTokens: 200, OutputTokens: 100, Requests: 2},
			},
		},
		tz:   time.UTC,
		calc: calc,
	}

	req := httptest.NewRequest("GET", "/ui/api/costs/timeseries?from=2026-03-01&to=2026-03-31&interval=week", nil)
	rec := httptest.NewRecorder()

	h.CostTimeseriesHandler(rec, req)

	require.Equal(t, 200, rec.Code)
	var resp costTimeseriesResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(t, resp.Data, 1)
	require.Equal(t, "2026-03-02", resp.Data[0].Date)
	require.Equal(t, int64(3), resp.Data[0].Requests)
	require.InDelta(t, 0.00255, resp.Data[0].CostUSD, 1e-9)
}

func TestAggregatePricedTimeseriesAggregatesDailyEntries(t *testing.T) {
	entries := []pricedTimeseriesEntry{
		{Date: "2026-03-13", CostUSD: 3.68, Requests: 1},
		{Date: "2026-03-13", CostUSD: 87.32, Requests: 2},
		{Date: "2026-03-14", CostUSD: 18.64, Requests: 3},
	}

	result := aggregatePricedTimeseries(entries, "day")

	require.Len(t, result, 2)
	require.Equal(t, "2026-03-13", result[0].Date)
	require.InDelta(t, 91.0, result[0].CostUSD, 1e-9)
	require.Equal(t, int64(3), result[0].Requests)
	require.Equal(t, "2026-03-14", result[1].Date)
	require.InDelta(t, 18.64, result[1].CostUSD, 1e-9)
	require.Equal(t, int64(3), result[1].Requests)
}
