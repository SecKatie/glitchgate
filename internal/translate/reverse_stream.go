// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ReverseSSEStream reads OpenAI SSE events from upstream, translates each to
// Anthropic-compatible SSE events, and writes them to the client. This is the
// reverse of SSEStream and is used when an Anthropic-format client sends a
// streaming request to an OpenAI-native provider.
func ReverseSSEStream(w http.ResponseWriter, upstream io.ReadCloser, model string) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()

	rc := http.NewResponseController(w)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var captured bytes.Buffer
	var inputTokens, outputTokens int64
	sentMessageStart := false
	sentContentBlockStart := false
	messageID := ""

	scanner := bufio.NewScanner(upstream)
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]

		// Handle the [DONE] sentinel.
		if data == "[DONE]" {
			// Send content_block_stop, message_delta, and message_stop.
			if sentContentBlockStart {
				if err := writeAnthropicSSE(w, rc, &captured, "content_block_stop",
					map[string]interface{}{"type": "content_block_stop", "index": 0}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, 0), err
				}
			}

			stopReason := "end_turn"
			if err := writeAnthropicSSE(w, rc, &captured, "message_delta",
				map[string]interface{}{
					"type":  "message_delta",
					"delta": map[string]interface{}{"stop_reason": stopReason},
					"usage": map[string]interface{}{"output_tokens": outputTokens},
				}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, 0), err
			}

			if err := writeAnthropicSSE(w, rc, &captured, "message_stop",
				map[string]interface{}{"type": "message_stop"}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, 0), err
			}
			continue
		}

		// Parse the OpenAI chunk.
		var chunk ChatCompletionResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if chunk.ID != "" && messageID == "" {
			messageID = strings.TrimPrefix(chunk.ID, "chatcmpl-")
		}

		// Extract usage from final chunk.
		if chunk.Usage != nil {
			inputTokens = chunk.Usage.PromptTokens
			outputTokens = chunk.Usage.CompletionTokens
		}

		// Send message_start on first chunk.
		if !sentMessageStart {
			if err := writeAnthropicSSE(w, rc, &captured, "message_start",
				map[string]interface{}{
					"type": "message_start",
					"message": map[string]interface{}{
						"id":      messageID,
						"type":    "message",
						"role":    "assistant",
						"content": []interface{}{},
						"model":   model,
						"usage":   map[string]interface{}{"input_tokens": 0, "output_tokens": 0},
					},
				}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, 0), err
			}
			sentMessageStart = true
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]
		delta := choice.Delta
		if delta == nil {
			continue
		}

		// Handle text content deltas.
		if text, ok := delta.Content.(string); ok && text != "" {
			if !sentContentBlockStart {
				if err := writeAnthropicSSE(w, rc, &captured, "content_block_start",
					map[string]interface{}{
						"type":          "content_block_start",
						"index":         0,
						"content_block": map[string]interface{}{"type": "text", "text": ""},
					}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, 0), err
				}
				sentContentBlockStart = true
			}

			if err := writeAnthropicSSE(w, rc, &captured, "content_block_delta",
				map[string]interface{}{
					"type":  "content_block_delta",
					"index": 0,
					"delta": map[string]interface{}{"type": "text_delta", "text": text},
				}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, 0), err
			}
		}

		// Handle finish_reason — update stop_reason for the final message_delta.
		if choice.FinishReason != nil {
			// The stop reason will be sent with the [DONE] handling above.
			continue
		}
	}

	return buildResult(&captured, inputTokens, outputTokens, 0, 0), scanner.Err()
}

// writeAnthropicSSE writes a single Anthropic-format SSE event to the client.
func writeAnthropicSSE(w http.ResponseWriter, rc *http.ResponseController, captured *bytes.Buffer, event string, data interface{}) error {
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
