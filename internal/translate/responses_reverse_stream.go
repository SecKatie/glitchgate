// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// ResponsesSSEToAnthropicSSE reads Responses API SSE events from upstream,
// translates each to Anthropic SSE events, and writes them to the client.
func ResponsesSSEToAnthropicSSE(w http.ResponseWriter, upstream io.ReadCloser, model string) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()

	rc := http.NewResponseController(w)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var captured bytes.Buffer
	var inputTokens, outputTokens, cacheReadTokens int64
	sentMessageStart := false
	sentStop := false
	messageID := ""
	openBlocks := make(map[int]string)

	scanner := newSSEScanner(upstream)
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]
		if data == "[DONE]" {
			continue
		}

		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			slog.Warn("failed to parse Responses API SSE event type", "error", err, "data", data[:min(len(data), 256)])
			continue
		}

		switch envelope.Type {
		case "response.created":
			var event struct {
				Response struct {
					ID    string `json:"id"`
					Model string `json:"model"`
				} `json:"response"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				slog.Warn("failed to parse response.created event", "error", err)
				continue
			}
			messageID = strings.TrimPrefix(event.Response.ID, "resp_")

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
					return buildResult(&captured, inputTokens, outputTokens, 0, 0, 0), err
				}
				sentMessageStart = true
			}

		case "response.output_item.added":
			var event struct {
				OutputIndex int `json:"output_index"`
				Item        struct {
					Type string `json:"type"`
				} `json:"item"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				slog.Warn("failed to parse response.output_item.added event", "error", err)
				continue
			}

			if event.Item.Type == "reasoning" && openBlocks[event.OutputIndex] == "" {
				if err := writeAnthropicSSE(w, rc, &captured, "content_block_start",
					map[string]interface{}{
						"type":          "content_block_start",
						"index":         event.OutputIndex,
						"content_block": map[string]interface{}{"type": "thinking", "thinking": ""},
					}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, 0, 0), err
				}
				openBlocks[event.OutputIndex] = "thinking"
			}

		case "response.content_part.added":
			var event struct {
				OutputIndex int `json:"output_index"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				slog.Warn("failed to parse response.content_part.added event", "error", err)
				continue
			}

			if openBlocks[event.OutputIndex] == "" {
				if err := writeAnthropicSSE(w, rc, &captured, "content_block_start",
					map[string]interface{}{
						"type":          "content_block_start",
						"index":         event.OutputIndex,
						"content_block": map[string]interface{}{"type": "text", "text": ""},
					}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, 0, 0), err
				}
				openBlocks[event.OutputIndex] = "text"
			}

		case "response.output_text.delta":
			var event struct {
				OutputIndex int    `json:"output_index"`
				Delta       string `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				slog.Warn("failed to parse response.output_text.delta event", "error", err)
				continue
			}

			if openBlocks[event.OutputIndex] == "" {
				if err := writeAnthropicSSE(w, rc, &captured, "content_block_start",
					map[string]interface{}{
						"type":          "content_block_start",
						"index":         event.OutputIndex,
						"content_block": map[string]interface{}{"type": "text", "text": ""},
					}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, 0, 0), err
				}
				openBlocks[event.OutputIndex] = "text"
			}

			if event.Delta != "" {
				if err := writeAnthropicSSE(w, rc, &captured, "content_block_delta",
					map[string]interface{}{
						"type":  "content_block_delta",
						"index": event.OutputIndex,
						"delta": map[string]interface{}{"type": "text_delta", "text": event.Delta},
					}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, 0, 0), err
				}
			}

		case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
			var event struct {
				OutputIndex int `json:"output_index"`
				Part        struct {
					Text string `json:"text"`
				} `json:"part"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				slog.Warn("failed to parse response.reasoning_summary_part event", "error", err)
				continue
			}

			if openBlocks[event.OutputIndex] == "" {
				if err := writeAnthropicSSE(w, rc, &captured, "content_block_start",
					map[string]interface{}{
						"type":          "content_block_start",
						"index":         event.OutputIndex,
						"content_block": map[string]interface{}{"type": "thinking", "thinking": ""},
					}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, 0, 0), err
				}
				openBlocks[event.OutputIndex] = "thinking"
			}

			if event.Part.Text != "" {
				if err := writeAnthropicSSE(w, rc, &captured, "content_block_delta",
					map[string]interface{}{
						"type":  "content_block_delta",
						"index": event.OutputIndex,
						"delta": map[string]interface{}{"type": "thinking_delta", "thinking": event.Part.Text},
					}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, 0, 0), err
				}
			}

		case "response.content_part.done", "response.output_text.done":
			var event struct {
				OutputIndex int `json:"output_index"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				slog.Warn("failed to parse response.content_part.done event", "error", err)
				continue
			}

			if openBlocks[event.OutputIndex] != "" {
				if err := writeAnthropicSSE(w, rc, &captured, "content_block_stop",
					map[string]interface{}{"type": "content_block_stop", "index": event.OutputIndex}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, 0, 0), err
				}
				delete(openBlocks, event.OutputIndex)
			}

		case "response.output_item.done":
			var event struct {
				OutputIndex int `json:"output_index"`
				Item        struct {
					Type string `json:"type"`
				} `json:"item"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				slog.Warn("failed to parse response.output_item.done event", "error", err)
				continue
			}

			if event.Item.Type == "reasoning" && openBlocks[event.OutputIndex] != "" {
				if err := writeAnthropicSSE(w, rc, &captured, "content_block_stop",
					map[string]interface{}{"type": "content_block_stop", "index": event.OutputIndex}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, 0, 0), err
				}
				delete(openBlocks, event.OutputIndex)
			}

		case "response.completed":
			var event struct {
				Response struct {
					Usage *struct {
						InputTokens        int64               `json:"input_tokens"`
						OutputTokens       int64               `json:"output_tokens"`
						InputTokensDetails *InputTokensDetails `json:"input_tokens_details"`
					} `json:"usage"`
				} `json:"response"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				slog.Warn("failed to parse response.completed event", "error", err)
			} else if event.Response.Usage != nil {
				inputTokens = event.Response.Usage.InputTokens
				outputTokens = event.Response.Usage.OutputTokens
				if event.Response.Usage.InputTokensDetails != nil {
					cacheReadTokens = event.Response.Usage.InputTokensDetails.CachedTokens
				}
			}

			sentStop = true
			stopReason := "end_turn"
			if err := writeAnthropicSSE(w, rc, &captured, "message_delta",
				map[string]interface{}{
					"type":  "message_delta",
					"delta": map[string]interface{}{"stop_reason": stopReason},
					"usage": map[string]interface{}{"output_tokens": outputTokens},
				}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, 0), err
			}

			if err := writeAnthropicSSE(w, rc, &captured, "message_stop",
				map[string]interface{}{"type": "message_stop"}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, 0), err
			}
		}
	}

	if sentMessageStart && !sentStop {
		slog.Warn("upstream Responses API stream ended without response.completed")
	}

	return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, 0), scanner.Err()
}

// ResponsesSSEToOpenAISSE reads Responses API SSE events from upstream,
// translates each to Chat Completions SSE events, and writes them to the client.
func ResponsesSSEToOpenAISSE(w http.ResponseWriter, upstream io.ReadCloser, model string) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()

	rc := http.NewResponseController(w)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var captured bytes.Buffer
	var inputTokens, outputTokens, cacheReadTokens int64
	messageID := ""
	var created int64
	sentInitial := false
	sentStop := false

	scanner := newSSEScanner(upstream)
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]
		if data == "[DONE]" {
			// Write [DONE] sentinel.
			done := "data: [DONE]\n\n"
			captured.WriteString(done)
			if _, err := w.Write([]byte(done)); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, 0, 0), err
			}
			if err := rc.Flush(); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, 0, 0), err
			}
			continue
		}

		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			slog.Warn("failed to parse Responses API SSE event type", "error", err, "data", data[:min(len(data), 256)])
			continue
		}

		switch envelope.Type {
		case "response.created":
			var event struct {
				Response struct {
					ID        string  `json:"id"`
					CreatedAt float64 `json:"created_at"`
				} `json:"response"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				slog.Warn("failed to parse response.created event", "error", err)
				continue
			}
			messageID = "chatcmpl-" + strings.TrimPrefix(event.Response.ID, "resp_")
			created = int64(event.Response.CreatedAt)

			// Send initial chunk with role.
			if !sentInitial {
				chunk := buildChunk(messageID, model, created, &ChatMessage{
					Role:    "assistant",
					Content: "",
				}, nil)
				if err := writeSSEChunk(w, rc, &captured, chunk); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, 0, 0), err
				}
				sentInitial = true
			}

		case "response.output_text.delta":
			var event struct {
				Delta string `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				slog.Warn("failed to parse response.output_text.delta event", "error", err)
				continue
			}

			if event.Delta != "" {
				chunk := buildChunk(messageID, model, created, &ChatMessage{
					Content: event.Delta,
				}, nil)
				if err := writeSSEChunk(w, rc, &captured, chunk); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, 0, 0), err
				}
			}

		case "response.completed":
			var event struct {
				Response struct {
					Status string `json:"status"`
					Usage  *struct {
						InputTokens        int64               `json:"input_tokens"`
						OutputTokens       int64               `json:"output_tokens"`
						InputTokensDetails *InputTokensDetails `json:"input_tokens_details"`
					} `json:"usage"`
				} `json:"response"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				slog.Warn("failed to parse response.completed event", "error", err)
			} else if event.Response.Usage != nil {
				inputTokens = event.Response.Usage.InputTokens
				outputTokens = event.Response.Usage.OutputTokens
				if event.Response.Usage.InputTokensDetails != nil {
					cacheReadTokens = event.Response.Usage.InputTokensDetails.CachedTokens
				}
			}

			sentStop = true
			finishReason := "stop"
			chunk := buildChunk(messageID, model, created, &ChatMessage{}, &finishReason)
			if err := writeSSEChunk(w, rc, &captured, chunk); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, 0), err
			}

			// Write [DONE].
			done := "data: [DONE]\n\n"
			captured.WriteString(done)
			if _, writeErr := w.Write([]byte(done)); writeErr != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, 0), writeErr
			}
			if flushErr := rc.Flush(); flushErr != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, 0), flushErr
			}
		}
	}

	if sentInitial && !sentStop {
		slog.Warn("upstream Responses API stream ended without response.completed")
	}

	return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, 0), scanner.Err()
}
