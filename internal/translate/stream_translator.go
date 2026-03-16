package translate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
)

// maxSSELineSize is the maximum size of a single SSE line we support.
// Anthropic's extended thinking produces signature_delta events that encode
// the full thinking content as a base64 signature, which can easily exceed
// bufio.Scanner's default 64 KB limit. 1 MB handles realistic thinking sizes.
const maxSSELineSize = 1 << 20 // 1 MB

// newSSEScanner creates a bufio.Scanner with a buffer large enough for
// extended thinking signature_delta events.
func newSSEScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, maxSSELineSize), maxSSELineSize)
	return s
}

// StreamResult holds the captured data from a translated streaming response.
type StreamResult struct {
	Body                     []byte
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	ReasoningTokens          int64
}

// SSEStream reads Anthropic SSE events from upstream, translates
// each to OpenAI-compatible SSE chunks, and writes them to the client.
// It returns a StreamResult with the captured output and token usage.
// SSEStream reads Anthropic SSE events from upstream, translates
// each to OpenAI-compatible SSE chunks, and writes them to the client.
// It returns a StreamResult with the captured output and token usage.
func SSEStream(w http.ResponseWriter, upstream io.ReadCloser, model string) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()

	rc := http.NewResponseController(w)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var captured bytes.Buffer
	var inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64
	created := time.Now().Unix()
	messageID := ""
	sentInitial := false
	sentStop := false
	thinkingBlockIndices := make(map[int]bool)

	scanner := newSSEScanner(upstream)
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]

		// Quick type check to determine event type.
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			slog.Warn("failed to parse Anthropic SSE event type", "error", err, "data", data[:min(len(data), 256)])
			continue
		}

		switch envelope.Type {
		case "message_start":
			var event anthropic.MessageStartEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				slog.Warn("failed to parse message_start event", "error", err)
				continue
			}
			messageID = "chatcmpl-" + event.Message.ID
			if event.Message.Usage.InputTokens > 0 {
				inputTokens = event.Message.Usage.InputTokens
			}
			cacheCreationTokens = event.Message.Usage.CacheCreationInputTokens
			cacheReadTokens = event.Message.Usage.CacheReadInputTokens

			// Send initial chunk with role.
			if !sentInitial {
				chunk := buildChunk(messageID, model, created, &ChatMessage{
					Role:    "assistant",
					Content: "",
				}, nil)
				if err := writeSSEChunk(w, rc, &captured, chunk); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
				}
				sentInitial = true
			}

		case "content_block_start":
			// Track thinking blocks by index so we can skip their deltas.
			var cbEvent anthropic.ContentBlockStartEvent
			if err := json.Unmarshal([]byte(data), &cbEvent); err != nil {
				slog.Warn("failed to parse content_block_start event", "error", err)
			} else if cbEvent.ContentBlock.Type == "thinking" {
				thinkingBlockIndices[cbEvent.Index] = true
			}

		case "content_block_delta":
			var event anthropic.ContentBlockDeltaEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				slog.Warn("failed to parse content_block_delta event", "error", err)
				continue
			}

			// Skip thinking block deltas — OpenAI CC doesn't stream reasoning.
			if thinkingBlockIndices[event.Index] {
				continue
			}

			switch event.Delta.Type {
			case "text_delta":
				chunk := buildChunk(messageID, model, created, &ChatMessage{
					Content: event.Delta.Text,
				}, nil)
				if err := writeSSEChunk(w, rc, &captured, chunk); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
				}

			case "input_json_delta":
				// Tool call argument streaming - pass through as tool call delta.
				// This is a simplified handling; full tool streaming would need
				// more state tracking.
			}

		case "content_block_stop":
			// Check if this was a thinking block — if so, skip.
			var cbStopEvent struct {
				Index int `json:"index"`
			}
			if err := json.Unmarshal([]byte(data), &cbStopEvent); err == nil {
				if thinkingBlockIndices[cbStopEvent.Index] {
					continue
				}
			}

		case "message_delta":
			var event anthropic.MessageDeltaEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				slog.Warn("failed to parse message_delta event", "error", err)
				continue
			}

			if event.Usage != nil {
				outputTokens = event.Usage.OutputTokens
			}

			finishReason := mapStopReason(event.Delta.StopReason)
			chunk := buildChunk(messageID, model, created, &ChatMessage{}, finishReason)
			if err := writeSSEChunk(w, rc, &captured, chunk); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
			}

		case "message_stop":
			sentStop = true
			// Write the [DONE] sentinel.
			done := "data: [DONE]\n\n"
			captured.WriteString(done)
			if _, err := w.Write([]byte(done)); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
			}
			if err := rc.Flush(); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
			}

		case "ping":
			// Ignore ping events.
		}
	}

	if sentInitial && !sentStop {
		slog.Warn("upstream Anthropic stream ended without message_stop")
	}

	return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), scanner.Err()
}

// buildChunk creates a ChatCompletionResponse formatted as a streaming chunk.
func buildChunk(id, model string, created int64, delta *ChatMessage, finishReason *string) *ChatCompletionResponse {
	return &ChatCompletionResponse{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []Choice{
			{
				Index:        0,
				Delta:        delta,
				FinishReason: finishReason,
			},
		},
	}
}

// writeSSEChunk serializes a chunk as an SSE data line and flushes it to the client.
func writeSSEChunk(w http.ResponseWriter, rc *http.ResponseController, captured *bytes.Buffer, chunk *ChatCompletionResponse) error {
	data, err := json.Marshal(chunk)
	if err != nil {
		return fmt.Errorf("marshalling chunk: %w", err)
	}

	line := fmt.Sprintf("data: %s\n\n", data)
	captured.WriteString(line)

	if _, err := w.Write([]byte(line)); err != nil {
		return err
	}
	return rc.Flush()
}

// buildResult creates a StreamResult from the accumulated state.
func buildResult(captured *bytes.Buffer, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, reasoningTokens int64) *StreamResult {
	return &StreamResult{
		Body:                     captured.Bytes(),
		InputTokens:              inputTokens,
		OutputTokens:             outputTokens,
		CacheCreationInputTokens: cacheCreationTokens,
		CacheReadInputTokens:     cacheReadTokens,
		ReasoningTokens:          reasoningTokens,
	}
}
