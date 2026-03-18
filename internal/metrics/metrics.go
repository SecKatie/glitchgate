// SPDX-License-Identifier: AGPL-3.0-or-later

// Package metrics provides Prometheus instrumentation for glitchgate.
package metrics //nolint:revive // does not conflict with runtime/metrics in usage

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/seckatie/glitchgate/internal/store"
)

const namespace = "glitchgate"

// Enabled controls whether metrics are recorded. When false, all Record*
// functions return immediately. This is safe to read/write from a single
// goroutine at startup; after that it is read-only.
var Enabled = true

var (
	requestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "requests_total",
		Help:      "Total number of proxied requests.",
	}, []string{"model", "provider", "source_format", "status_code", "key_prefix"})

	requestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "request_duration_seconds",
		Help:      "Latency of proxied requests in seconds.",
		Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
	}, []string{"model", "provider", "source_format"})

	inputTokensTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "input_tokens_total",
		Help:      "Total input tokens consumed.",
	}, []string{"model", "provider"})

	outputTokensTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "output_tokens_total",
		Help:      "Total output tokens produced.",
	}, []string{"model", "provider"})

	cacheReadTokensTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "cache_read_tokens_total",
		Help:      "Total cache-read input tokens.",
	}, []string{"model", "provider"})

	cacheCreationTokensTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "cache_creation_tokens_total",
		Help:      "Total cache-creation input tokens.",
	}, []string{"model", "provider"})

	reasoningTokensTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "reasoning_tokens_total",
		Help:      "Total reasoning tokens consumed.",
	}, []string{"model", "provider"})

	costUSDTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "cost_usd_total",
		Help:      "Total estimated cost in USD.",
	}, []string{"model", "provider"})

	fallbackAttemptsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "fallback_attempts_total",
		Help:      "Total fallback attempts across requests.",
	}, []string{"model"})

	streamingRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "streaming_requests_total",
		Help:      "Total number of streaming requests.",
	}, []string{"model", "provider"})

	activeRequests = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "active_requests",
		Help:      "Number of in-flight proxy requests.",
	}, []string{"source_format"})

	asyncLoggerEnqueued = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "async_logger_enqueued_total",
		Help:      "Total entries enqueued to the async logger.",
	})

	asyncLoggerPersisted = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "async_logger_persisted_total",
		Help:      "Total entries persisted by the async logger.",
	})

	asyncLoggerDropped = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "async_logger_dropped_total",
		Help:      "Total entries dropped by the async logger.",
	})

	asyncLoggerFailed = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "async_logger_failed_total",
		Help:      "Total entries that failed to persist.",
	})
)

func init() {
	prometheus.MustRegister(
		requestsTotal,
		requestDuration,
		inputTokensTotal,
		outputTokensTotal,
		cacheReadTokensTotal,
		cacheCreationTokensTotal,
		reasoningTokensTotal,
		costUSDTotal,
		fallbackAttemptsTotal,
		streamingRequestsTotal,
		activeRequests,
		asyncLoggerEnqueued,
		asyncLoggerPersisted,
		asyncLoggerDropped,
		asyncLoggerFailed,
	)
}

// RecordRequest records all per-request metrics from a completed log entry.
func RecordRequest(entry *store.RequestLogEntry, keyPrefix string) {
	if !Enabled {
		return
	}

	model := entry.ModelRequested
	prov := entry.ProviderName
	src := entry.SourceFormat
	status := fmt.Sprintf("%d", entry.Status)

	requestsTotal.WithLabelValues(model, prov, src, status, keyPrefix).Inc()
	requestDuration.WithLabelValues(model, prov, src).Observe(float64(entry.LatencyMs) / 1000.0)

	if entry.InputTokens > 0 {
		inputTokensTotal.WithLabelValues(model, prov).Add(float64(entry.InputTokens))
	}
	if entry.OutputTokens > 0 {
		outputTokensTotal.WithLabelValues(model, prov).Add(float64(entry.OutputTokens))
	}
	if entry.CacheReadInputTokens > 0 {
		cacheReadTokensTotal.WithLabelValues(model, prov).Add(float64(entry.CacheReadInputTokens))
	}
	if entry.CacheCreationInputTokens > 0 {
		cacheCreationTokensTotal.WithLabelValues(model, prov).Add(float64(entry.CacheCreationInputTokens))
	}
	if entry.ReasoningTokens > 0 {
		reasoningTokensTotal.WithLabelValues(model, prov).Add(float64(entry.ReasoningTokens))
	}
	if entry.CostUSD != nil && *entry.CostUSD > 0 {
		costUSDTotal.WithLabelValues(model, prov).Add(*entry.CostUSD)
	}
	if entry.FallbackAttempts > 1 {
		fallbackAttemptsTotal.WithLabelValues(model).Add(float64(entry.FallbackAttempts))
	}
	if entry.IsStreaming {
		streamingRequestsTotal.WithLabelValues(model, prov).Inc()
	}
}

// RecordActiveRequest increments the active request gauge for a source format.
func RecordActiveRequest(sourceFormat string) {
	if !Enabled {
		return
	}
	activeRequests.WithLabelValues(sourceFormat).Inc()
}

// FinishActiveRequest decrements the active request gauge for a source format.
func FinishActiveRequest(sourceFormat string) {
	if !Enabled {
		return
	}
	activeRequests.WithLabelValues(sourceFormat).Dec()
}

// RecordLoggerStats updates the async logger gauge metrics.
func RecordLoggerStats(enqueued, persisted, dropped, failed uint64) {
	if !Enabled {
		return
	}
	asyncLoggerEnqueued.Set(float64(enqueued))
	asyncLoggerPersisted.Set(float64(persisted))
	asyncLoggerDropped.Set(float64(dropped))
	asyncLoggerFailed.Set(float64(failed))
}
