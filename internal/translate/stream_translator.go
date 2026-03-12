package translate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"codeberg.org/kglitchy/glitchgate/internal/provider/anthropic"
)

// StreamResult holds the captured data from a translated streaming response.
type StreamResult struct {
	Body                     []byte
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
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

	scanner := bufio.NewScanner(upstream)
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
			continue
		}

		switch envelope.Type {
		case "message_start":
			var event anthropic.MessageStartEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
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
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens), err
				}
				sentInitial = true
			}

		case "content_block_delta":
			var event anthropic.ContentBlockDeltaEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}

			switch event.Delta.Type {
			case "text_delta":
				chunk := buildChunk(messageID, model, created, &ChatMessage{
					Content: event.Delta.Text,
				}, nil)
				if err := writeSSEChunk(w, rc, &captured, chunk); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens), err
				}

			case "input_json_delta":
				// Tool call argument streaming - pass through as tool call delta.
				// This is a simplified handling; full tool streaming would need
				// more state tracking.
			}

		case "message_delta":
			var event anthropic.MessageDeltaEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}

			if event.Usage != nil {
				outputTokens = event.Usage.OutputTokens
			}

			finishReason := mapStopReason(event.Delta.StopReason)
			chunk := buildChunk(messageID, model, created, &ChatMessage{}, finishReason)
			if err := writeSSEChunk(w, rc, &captured, chunk); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens), err
			}

		case "message_stop":
			// Write the [DONE] sentinel.
			done := "data: [DONE]\n\n"
			captured.WriteString(done)
			if _, err := w.Write([]byte(done)); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens), err
			}
			if err := rc.Flush(); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens), err
			}

		case "ping":
			// Ignore ping events.
		}
	}

	return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens), scanner.Err()
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
func buildResult(captured *bytes.Buffer, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64) *StreamResult {
	return &StreamResult{
		Body:                     captured.Bytes(),
		InputTokens:              inputTokens,
		OutputTokens:             outputTokens,
		CacheCreationInputTokens: cacheCreationTokens,
		CacheReadInputTokens:     cacheReadTokens,
	}
}
