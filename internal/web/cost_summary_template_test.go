// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"codeberg.org/kglitchy/glitchgate/internal/store"
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
		},
	})

	require.NoError(t, err)
	body := rec.Body.String()

	require.Contains(t, body, "$10.000000 <span class=\"token-detail-note\">(100%)</span>")
	require.Contains(t, body, "$1.000000 <span class=\"token-detail-note\">(10.0%)</span>")
	require.Contains(t, body, "$3.000000 <span class=\"token-detail-note\">(30.0%)</span>")
	require.Contains(t, body, "$6.000000 <span class=\"token-detail-note\">(60.0%)</span>")
	require.Contains(t, body, "$4.000000 <span class=\"token-detail-note\">(100%)</span>")
	require.Contains(t, body, "Per-category costs are partial because some models in this filtered result set do not have pricing configured.")
	require.False(t, strings.Contains(body, "(partial)</span>"))
}
