package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/seckatie/glitchgate/internal/provider"
	"github.com/seckatie/glitchgate/internal/sse"
)

// StreamResult is a type alias for sse.StreamResult.
type StreamResult = sse.StreamResult

// TokenExtractor parses an SSE data payload and updates token counts in result.
type TokenExtractor func(data string, result *StreamResult)

// streamingResult converts a StreamResult and error from a stream relay or
// translation function into a handlerResult.
func streamingResult(resp *provider.Response, result *sse.StreamResult, err error) handlerResult {
	var errDetails *string
	if err != nil {
		errDetails = streamRelayErrorDetails(err)
		if errDetails != nil {
			slog.Warn("stream relay error", "error", err)
		}
	}

	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	return handlerResult{
		InputTokens:              result.InputTokens,
		OutputTokens:             result.OutputTokens,
		CacheCreationInputTokens: result.CacheCreationInputTokens,
		CacheReadInputTokens:     result.CacheReadInputTokens,
		ReasoningTokens:          result.ReasoningTokens,
		Status:                   status,
		Body:                     result.Body,
		ErrDetails:               errDetails,
		IsStreaming:              true,
	}
}

// synthesizedStreamResult wraps the output of a Synthesize*SSE call into a
// handlerResult, using token counts from the provider response.
func synthesizedStreamResult(resp *provider.Response, result *sse.StreamResult, err error) handlerResult {
	var errDetails *string
	if err != nil {
		s := fmt.Sprintf("stream synthesis error: %v", err)
		errDetails = &s
		slog.Warn("stream synthesis error", "error", err)
	}
	return handlerResult{
		InputTokens:              resp.InputTokens,
		OutputTokens:             resp.OutputTokens,
		CacheCreationInputTokens: resp.CacheCreationInputTokens,
		CacheReadInputTokens:     resp.CacheReadInputTokens,
		ReasoningTokens:          resp.ReasoningTokens,
		Status:                   http.StatusOK,
		Body:                     result.Body,
		ErrDetails:               errDetails,
		IsStreaming:              true,
	}
}

// RelaySSEStream reads SSE events from upstream and forwards them to the client,
// flushing after each event. It captures the full stream and calls extract on
// each data payload to accumulate token usage. When ctx is cancelled (e.g.
// client disconnect), the upstream reader is closed to unblock the scanner.
//
// Use one of the format-specific extractors: extractAnthropicTokens,
// extractOpenAITokens, or extractResponsesTokens.
func RelaySSEStream(ctx context.Context, w http.ResponseWriter, upstream io.ReadCloser, extract TokenExtractor) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()
	stop := sse.CloseOnCancel(ctx, upstream)
	defer stop()

	rc := http.NewResponseController(w)

	sse.WriteHeaders(w)

	var captured bytes.Buffer
	var result StreamResult

	scanner := sse.NewScanner(upstream)
	for scanner.Scan() {
		line := sanitizeSSELine(scanner.Text())
		captured.WriteString(line)
		captured.WriteByte('\n')

		if strings.HasPrefix(line, "data: ") {
			data := line[6:]
			extract(data, &result)
		}

		// Write the line to the client.
		if _, err := w.Write([]byte(line)); err != nil {
			result.Body = captured.Bytes()
			return &result, err
		}
		if _, err := w.Write([]byte{'\n'}); err != nil {
			result.Body = captured.Bytes()
			return &result, err
		}

		// Flush after blank lines (SSE event boundary).
		if line == "" {
			if err := rc.Flush(); err != nil {
				result.Body = captured.Bytes()
				return &result, err
			}
		}
	}

	result.Body = captured.Bytes()
	return &result, scanner.Err()
}

// sanitizeSSELine strips carriage returns and null bytes from an SSE line as
// defense-in-depth. bufio.Scanner already strips trailing \r, but embedded \r
// or \x00 from a misbehaving upstream could confuse downstream consumers.
func sanitizeSSELine(line string) string {
	if !strings.ContainsAny(line, "\r\x00") {
		return line
	}
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == 0 {
			return -1
		}
		return r
	}, line)
}

// SkipDone wraps an extractor to skip the "[DONE]" sentinel.
func SkipDone(extract TokenExtractor) TokenExtractor {
	return func(data string, result *StreamResult) {
		if data != "[DONE]" {
			extract(data, result)
		}
	}
}
