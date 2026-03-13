// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"codeberg.org/kglitchy/glitchgate/internal/pricing"
	"codeberg.org/kglitchy/glitchgate/internal/store"
)

func TestLogDetailTemplateShowsTokenAndCostPercentages(t *testing.T) {
	entry := pricing.Entry{
		InputPerMillion:      2.50,
		OutputPerMillion:     15.00,
		CacheWritePerMillion: 3.75,
		CacheReadPerMillion:  0.25,
	}
	provKey := "anthropic:api.anthropic.com"

	logEntry := &store.RequestLogDetail{
		RequestLogSummary: store.RequestLogSummary{
			ID:                   "log_123",
			Timestamp:            time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC),
			Status:               200,
			ProviderName:         provKey,
			ModelRequested:       "claude-test",
			ModelUpstream:        "claude-test",
			ProxyKeyPrefix:       "llmp_sk_test",
			ProxyKeyLabel:        "Test Key",
			InputTokens:          1108,
			OutputTokens:         295,
			CacheReadInputTokens: 20608,
			ReasoningTokens:      177,
			LatencyMs:            1234,
			SourceFormat:         "anthropic",
		},
	}

	cost := computeCostBreakdown(logEntry, makeCalc(provKey, "claude-test", entry))
	templates := ParseTemplates(time.UTC)
	rec := httptest.NewRecorder()

	err := templates.ExecuteTemplate(rec, "log_detail.html", LogDetailData{
		ActiveTab: "logs",
		Log:       logEntry,
		Cost:      cost,
	})

	require.NoError(t, err)
	body := rec.Body.String()

	require.Contains(t, body, "1108 <span class=\"token-detail-note\">(5.1% of total input)</span>")
	require.Contains(t, body, "20608 <span class=\"token-detail-note\">(94.9% of total input)</span>")
	require.Contains(t, body, "177 <span class=\"token-detail-note\">(60.0% of output)</span>")
	require.Contains(t, body, "$0.002770 <span class=\"token-detail-note\">(35.0%)</span>")
	require.Contains(t, body, "$0.005152 <span class=\"token-detail-note\">(65.0%)</span>")
	require.Contains(t, body, "$0.002655 <span class=\"token-detail-note\">(60.0%)</span>")
	require.True(t, strings.Contains(body, "295 <span class=\"token-detail-note\">(100%)</span>"))
}
