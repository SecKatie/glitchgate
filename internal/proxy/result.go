// SPDX-License-Identifier: AGPL-3.0-or-later

package proxy

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"codeberg.org/kglitchy/glitchgate/internal/pricing"
	"codeberg.org/kglitchy/glitchgate/internal/store"
)

// handlerResult captures the outcome of writing an upstream response to the client.
// All handle* functions return this; callers use it to emit exactly one log entry.
type handlerResult struct {
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	ReasoningTokens          int64
	Status                   int
	Body                     []byte
	ErrDetails               *string
	IsStreaming              bool
}

// logEntry records one request/response cycle to the database.
// This is the single log call site; individual handle* functions never call it directly.
func (l *AsyncLogger) logEntry(
	proxyKeyID, sourceFormat, providerName, modelRequested, modelUpstream, resolvedModelName string,
	latencyMs int64, requestBody []byte, attemptCount int64, r handlerResult,
	calc *pricing.Calculator,
) {
	// If resolvedModelName not provided, default to modelUpstream if fallback happened, else modelRequested
	if resolvedModelName == "" {
		if attemptCount > 1 && modelUpstream != "" {
			resolvedModelName = modelUpstream
		} else {
			resolvedModelName = modelRequested
		}
	}

	var costUSD *float64
	if calc != nil {
		costUSD = calc.Calculate(providerName, modelUpstream,
			r.InputTokens, r.OutputTokens,
			r.CacheCreationInputTokens, r.CacheReadInputTokens,
			r.ReasoningTokens)
	}

	l.Log(&store.RequestLogEntry{
		ID:                       uuid.New().String(),
		ProxyKeyID:               proxyKeyID,
		Timestamp:                time.Now().UTC(),
		SourceFormat:             sourceFormat,
		ProviderName:             providerName,
		ModelRequested:           modelRequested,
		ModelUpstream:            modelUpstream,
		ResolvedModelName:        resolvedModelName,
		InputTokens:              r.InputTokens,
		OutputTokens:             r.OutputTokens,
		CacheCreationInputTokens: r.CacheCreationInputTokens,
		CacheReadInputTokens:     r.CacheReadInputTokens,
		ReasoningTokens:          r.ReasoningTokens,
		LatencyMs:                latencyMs,
		Status:                   r.Status,
		RequestBody:              truncateLoggedBody(RedactRequestBody(requestBody), l.bodyMaxBytes),
		ResponseBody:             truncateLoggedBody(string(r.Body), l.bodyMaxBytes),
		ErrorDetails:             r.ErrDetails,
		IsStreaming:              r.IsStreaming,
		FallbackAttempts:         attemptCount,
		CostUSD:                  costUSD,
	})
}

func truncateLoggedBody(body string, maxBytes int) string {
	if maxBytes <= 0 || len(body) <= maxBytes {
		return body
	}
	return fmt.Sprintf("%s\n[TRUNCATED original_bytes=%d]", body[:maxBytes], len(body))
}
