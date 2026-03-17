// SPDX-License-Identifier: AGPL-3.0-or-later

package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/store"
)

func TestRecordRequest(t *testing.T) {
	cost := 0.05
	entry := &store.RequestLogEntry{
		ModelRequested:           "claude-sonnet-4-6",
		ProviderName:             "anthropic",
		SourceFormat:             "anthropic",
		Status:                   200,
		LatencyMs:                2500,
		InputTokens:              100,
		OutputTokens:             50,
		CacheReadInputTokens:     10,
		CacheCreationInputTokens: 5,
		ReasoningTokens:          20,
		IsStreaming:              true,
		FallbackAttempts:         2,
		CostUSD:                  &cost,
	}

	RecordRequest(entry, "llmp_sk_abc")

	require.Equal(t, float64(1), testutil.ToFloat64(requestsTotal.WithLabelValues("claude-sonnet-4-6", "anthropic", "anthropic", "200", "llmp_sk_abc")))
	require.Equal(t, float64(100), testutil.ToFloat64(inputTokensTotal.WithLabelValues("claude-sonnet-4-6", "anthropic")))
	require.Equal(t, float64(50), testutil.ToFloat64(outputTokensTotal.WithLabelValues("claude-sonnet-4-6", "anthropic")))
	require.Equal(t, float64(10), testutil.ToFloat64(cacheReadTokensTotal.WithLabelValues("claude-sonnet-4-6", "anthropic")))
	require.Equal(t, float64(5), testutil.ToFloat64(cacheCreationTokensTotal.WithLabelValues("claude-sonnet-4-6", "anthropic")))
	require.Equal(t, float64(20), testutil.ToFloat64(reasoningTokensTotal.WithLabelValues("claude-sonnet-4-6", "anthropic")))
	require.Equal(t, 0.05, testutil.ToFloat64(costUSDTotal.WithLabelValues("claude-sonnet-4-6", "anthropic")))
	require.Equal(t, float64(2), testutil.ToFloat64(fallbackAttemptsTotal.WithLabelValues("claude-sonnet-4-6")))
	require.Equal(t, float64(1), testutil.ToFloat64(streamingRequestsTotal.WithLabelValues("claude-sonnet-4-6", "anthropic")))

	// Verify histogram was observed (check the counter part of the histogram).
	require.Equal(t, 1, testutil.CollectAndCount(requestDuration))
}

func TestRecordRequestDisabled(_ *testing.T) {
	Enabled = false
	defer func() { Enabled = true }()

	entry := &store.RequestLogEntry{
		ModelRequested: "test-model",
		ProviderName:   "test",
		SourceFormat:   "openai",
		Status:         200,
		InputTokens:    999,
	}

	// Should not panic or record anything.
	RecordRequest(entry, "test")
	RecordActiveRequest("openai")
	FinishActiveRequest("openai")
	RecordLoggerStats(1, 2, 3, 4)
}

func TestActiveRequestGauge(t *testing.T) {
	RecordActiveRequest("anthropic")
	RecordActiveRequest("anthropic")
	require.Equal(t, float64(2), testutil.ToFloat64(activeRequests.WithLabelValues("anthropic")))

	FinishActiveRequest("anthropic")
	require.Equal(t, float64(1), testutil.ToFloat64(activeRequests.WithLabelValues("anthropic")))

	FinishActiveRequest("anthropic")
	require.Equal(t, float64(0), testutil.ToFloat64(activeRequests.WithLabelValues("anthropic")))
}

func TestRecordLoggerStats(t *testing.T) {
	RecordLoggerStats(100, 95, 3, 2)

	require.Equal(t, float64(100), testutil.ToFloat64(asyncLoggerEnqueued))
	require.Equal(t, float64(95), testutil.ToFloat64(asyncLoggerPersisted))
	require.Equal(t, float64(3), testutil.ToFloat64(asyncLoggerDropped))
	require.Equal(t, float64(2), testutil.ToFloat64(asyncLoggerFailed))
}

func TestRecordRequestNoTokens(t *testing.T) {
	// Entry with zero tokens should not increment token counters beyond zero.
	entry := &store.RequestLogEntry{
		ModelRequested:   "empty-model",
		ProviderName:     "test",
		SourceFormat:     "openai",
		Status:           500,
		LatencyMs:        100,
		FallbackAttempts: 1,
	}

	RecordRequest(entry, "")
	require.Equal(t, float64(1), testutil.ToFloat64(requestsTotal.WithLabelValues("empty-model", "test", "openai", "500", "")))
	require.Equal(t, float64(0), testutil.ToFloat64(inputTokensTotal.WithLabelValues("empty-model", "test")))
}
