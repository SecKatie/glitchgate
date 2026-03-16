// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/store"
)

func TestCostSummaryTemplateKeepsPercentagesWhenPricingIsPartial(t *testing.T) {
	templates := ParseTemplates(time.UTC)
	rec := httptest.NewRecorder()

	err := templates.ExecuteNamed(rec, "cost_summary", map[string]any{
		"Summary": store.CostSummary{
			TotalInputTokens:         10,
			TotalCacheCreationTokens: 30,
			TotalCacheReadTokens:     60,
			TotalOutputTokens:        20,
		},
		"TotalAllInputTokens": int64(100),
		"TokenCosts": &AggregateCostBreakdown{
			PricingKnown:      false,
			PartialPricing:    true,
			HasAnyPricing:     true,
			TotalInputCostUSD: 10,
			InputCostUSD:      1,
			CacheWriteCostUSD: 3,
			CacheReadCostUSD:  6,
			OutputCostUSD:     4,
			TotalCostUSD:      14,
		},
	})

	require.NoError(t, err)
	body := rec.Body.String()

	require.Contains(t, body, "$10.000000 <span class=\"token-detail-note\">(71.4%)</span>")
	require.Contains(t, body, "$1.000000 <span class=\"token-detail-note\">(10.0%)</span>")
	require.Contains(t, body, "$3.000000 <span class=\"token-detail-note\">(30.0%)</span>")
	require.Contains(t, body, "$6.000000 <span class=\"token-detail-note\">(60.0%)</span>")
	require.Contains(t, body, "$4.000000 <span class=\"token-detail-note\">(28.6%)</span>")
	require.Contains(t, body, "Per-category costs are partial because some models in this filtered result set do not have pricing configured.")
	require.False(t, strings.Contains(body, "(partial)</span>"))
}

func TestCostSummaryTemplateShowsProviderLinkInsteadOfSubscriptionColumns(t *testing.T) {
	templates := ParseTemplates(time.UTC)
	rec := httptest.NewRecorder()

	err := templates.ExecuteNamed(rec, "cost_summary", map[string]any{
		"GroupBy": "provider",
		"Summary": store.CostSummary{
			TotalRequests:     2,
			TotalInputTokens:  100,
			TotalOutputTokens: 20,
		},
		"TotalAllInputTokens": int64(100),
		"TokenCosts": &AggregateCostBreakdown{
			HasAnyPricing: true,
			TotalCostUSD:  18.5,
		},
		"Breakdown": []store.CostBreakdownEntry{
			{Group: "chatgpt-pro", InputTokens: 100, OutputTokens: 20, Requests: 2},
		},
		"BreakdownCosts": map[string]float64{
			"chatgpt-pro": 18.5,
		},
		"ProviderComparisons": map[string]*ProviderSpendComparison{
			"chatgpt-pro": {
				MonthlySubscriptionCost: 20.0,
			},
		},
		"ProviderComparisonSummary": ProviderSpendComparisonSummary{
			HasAnySubscription:    true,
			TotalSubscriptionCost: 20.0,
		},
		"MaxBreakdownRequests": float64(2),
	})

	require.NoError(t, err)
	body := rec.Body.String()

	// Subscription columns should NOT be in the breakdown table.
	require.NotContains(t, body, "Monthly Subscription")
	require.NotContains(t, body, "Token vs Subscription")
	require.NotContains(t, body, "Effective Subscription $/MTok")

	// Instead, a link to the Providers page should be shown.
	require.Contains(t, body, "/ui/providers")
	require.Contains(t, body, "Provider subscription analysis has moved")
}

func TestCostsPageTemplatePushesFilterStateIntoURL(t *testing.T) {
	templates := ParseTemplates(time.UTC)
	rec := httptest.NewRecorder()

	err := templates.ExecuteTemplate(rec, "costs.html", map[string]any{
		"From":                "2026-03-01",
		"To":                  "2026-03-31",
		"GroupBy":             "provider",
		"GroupFilter":         "chatgpt",
		"Summary":             store.CostSummary{},
		"TokenCosts":          &AggregateCostBreakdown{},
		"TotalAllInputTokens": int64(0),
	})

	require.NoError(t, err)
	body := rec.Body.String()

	require.Contains(t, body, `action="/ui/costs"`)
	require.Contains(t, body, `method="get"`)
	require.Contains(t, body, `hx-get="/ui/costs"`)
	require.Contains(t, body, `hx-select="#cost-content"`)
	require.Contains(t, body, `hx-target="#cost-content"`)
	require.Contains(t, body, `hx-swap="outerHTML"`)
	require.Contains(t, body, `hx-push-url="true"`)
	require.Contains(t, body, `<option value="provider" selected>Provider</option>`)
	require.Contains(t, body, `value="chatgpt"`)
}
