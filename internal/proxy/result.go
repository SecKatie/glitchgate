// SPDX-License-Identifier: AGPL-3.0-or-later

package proxy

import (
	"fmt"
	"time"

	"github.com/google/uuid"

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
	proxyKeyID, sourceFormat, providerName, modelRequested, modelUpstream string,
	latencyMs int64, requestBody []byte, attemptCount int64, r handlerResult,
) {
	l.Log(&store.RequestLogEntry{
		ID:                       uuid.New().String(),
		ProxyKeyID:               proxyKeyID,
		Timestamp:                time.Now().UTC(),
		SourceFormat:             sourceFormat,
		ProviderName:             providerName,
		ModelRequested:           modelRequested,
		ModelUpstream:            modelUpstream,
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
	})
}

func truncateLoggedBody(body string, maxBytes int) string {
	if maxBytes <= 0 || len(body) <= maxBytes {
		return body
	}
	return fmt.Sprintf("%s\n[TRUNCATED original_bytes=%d]", body[:maxBytes], len(body))
}
