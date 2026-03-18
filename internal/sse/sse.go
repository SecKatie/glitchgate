// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sse provides shared primitives for Server-Sent Event streaming.
package sse

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// MaxLineSize is the maximum size of a single SSE line we support.
// Anthropic's extended thinking produces signature_delta events that encode
// the full thinking content as a base64 signature, which can easily exceed
// bufio.Scanner's default 64 KB limit. 1 MB handles realistic thinking sizes.
const MaxLineSize = 1 << 20 // 1 MB

// StreamResult holds the captured data from a streamed response.
type StreamResult struct {
	Body                     []byte
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	ReasoningTokens          int64
}

// NewScanner creates a bufio.Scanner with a buffer large enough for
// extended thinking signature_delta events.
func NewScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, MaxLineSize), MaxLineSize)
	return s
}

// CloseOnCancel starts a goroutine that closes r when ctx is cancelled,
// unblocking any in-progress reads (e.g. a bufio.Scanner). Returns a cleanup
// function that must be deferred to prevent a goroutine leak when the stream
// finishes normally before the context is cancelled.
func CloseOnCancel(ctx context.Context, r io.Closer) (stop func()) {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = r.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}

// WriteHeaders sets the standard SSE response headers and writes a 200 status.
func WriteHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

// WriteEvent writes a single SSE event (with event type and JSON data) to the
// client, appends it to captured, and flushes. This unifies the previously
// duplicated writeAnthropicSSE and writeResponsesSSE helpers.
func WriteEvent(w http.ResponseWriter, rc *http.ResponseController, captured *bytes.Buffer, event string, data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshalling SSE data: %w", err)
	}

	line := fmt.Sprintf("event: %s\ndata: %s\n\n", event, jsonData)
	captured.WriteString(line)

	if _, err := w.Write([]byte(line)); err != nil {
		return err
	}
	return rc.Flush()
}

// BuildResult creates a StreamResult from the captured buffer and token counts.
func BuildResult(captured *bytes.Buffer, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, reasoningTokens int64) *StreamResult {
	return &StreamResult{
		Body:                     captured.Bytes(),
		InputTokens:              inputTokens,
		OutputTokens:             outputTokens,
		CacheCreationInputTokens: cacheCreationTokens,
		CacheReadInputTokens:     cacheReadTokens,
		ReasoningTokens:          reasoningTokens,
	}
}
