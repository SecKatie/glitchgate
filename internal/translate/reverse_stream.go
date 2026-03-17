// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
)

// ReverseSSEStream reads OpenAI SSE events from upstream, translates each to
// Anthropic-compatible SSE events, and writes them to the client. This is the
// reverse of SSEStream and is used when an Anthropic-format client sends a
// streaming request to an OpenAI-native provider.
func ReverseSSEStream(ctx context.Context, w http.ResponseWriter, upstream io.ReadCloser, model string) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()
	stop := closeOnCancel(ctx, upstream)
	defer stop()

	rc := http.NewResponseController(w)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var captured bytes.Buffer
	var inputTokens, outputTokens, cacheReadTokens int64
	sentMessageStart := false
	sentDone := false
	messageID := ""
	finishReason := "end_turn"

	// Content block state.
	type toolCallState struct {
		id         string
		name       string
		blockIndex int
	}
	toolCalls := make(map[int]*toolCallState) // OAI index → state
	nextBlockIndex := 0
	textBlockIndex := -1 // -1 = no text block started yet

	scanner := newSSEScanner(upstream)
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]

		if data == "[DONE]" {
			// Close text block if open.
			if textBlockIndex >= 0 {
				if err := writeAnthropicSSE(w, rc, &captured, "content_block_stop",
					map[string]interface{}{"type": "content_block_stop", "index": textBlockIndex}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, 0), err
				}
			}

			// Close tool call blocks in ascending block-index order.
			type tcEntry struct {
				state *toolCallState
			}
			entries := make([]tcEntry, 0, len(toolCalls))
			for _, s := range toolCalls {
				entries = append(entries, tcEntry{s})
			}
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].state.blockIndex < entries[j].state.blockIndex
			})
			for _, e := range entries {
				if err := writeAnthropicSSE(w, rc, &captured, "content_block_stop",
					map[string]interface{}{"type": "content_block_stop", "index": e.state.blockIndex}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, 0), err
				}
			}

			// Map OpenAI finish_reason → Anthropic stop_reason.
			stopReason := "end_turn"
			if finishReason == "tool_calls" {
				stopReason = "tool_use"
			}

			if err := writeAnthropicSSE(w, rc, &captured, "message_delta",
				map[string]interface{}{
					"type":  "message_delta",
					"delta": map[string]interface{}{"stop_reason": stopReason},
					"usage": map[string]interface{}{
						"output_tokens":               outputTokens,
						"input_tokens":                inputTokens,
						"cache_creation_input_tokens": int64(0),
						"cache_read_input_tokens":     cacheReadTokens,
					},
				}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, 0), err
			}

			if err := writeAnthropicSSE(w, rc, &captured, "message_stop",
				map[string]interface{}{"type": "message_stop"}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, 0), err
			}

			sentDone = true
			continue
		}

		// Parse the OpenAI chunk.
		var chunk ChatCompletionResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			slog.Warn("failed to parse upstream SSE chunk", "error", err, "data", data[:min(len(data), 256)])
			continue
		}

		if chunk.ID != "" && messageID == "" {
			messageID = strings.TrimPrefix(chunk.ID, "chatcmpl-")
		}

		// Extract usage from final chunk.
		if chunk.Usage != nil {
			inputTokens = chunk.Usage.PromptTokens
			outputTokens = chunk.Usage.CompletionTokens
			if chunk.Usage.PromptTokensDetails != nil {
				cacheReadTokens = chunk.Usage.PromptTokensDetails.CachedTokens
			}
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
						"usage": map[string]interface{}{
							"input_tokens":                0,
							"output_tokens":               0,
							"cache_creation_input_tokens": 0,
							"cache_read_input_tokens":     0,
						},
					},
				}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, 0), err
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

		// Capture finish_reason for [DONE] handling.
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			finishReason = *choice.FinishReason
		}

		// Handle text content deltas.
		if text, ok := delta.Content.(string); ok && text != "" {
			if textBlockIndex < 0 {
				textBlockIndex = nextBlockIndex
				nextBlockIndex++
				if err := writeAnthropicSSE(w, rc, &captured, "content_block_start",
					map[string]interface{}{
						"type":          "content_block_start",
						"index":         textBlockIndex,
						"content_block": map[string]interface{}{"type": "text", "text": ""},
					}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, 0), err
				}
			}

			if err := writeAnthropicSSE(w, rc, &captured, "content_block_delta",
				map[string]interface{}{
					"type":  "content_block_delta",
					"index": textBlockIndex,
					"delta": map[string]interface{}{"type": "text_delta", "text": text},
				}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, 0), err
			}
		}

		// Handle tool call deltas.
		for _, tc := range delta.ToolCalls {
			if tc.Index == nil {
				continue
			}
			oaiIdx := *tc.Index

			state, exists := toolCalls[oaiIdx]
			if !exists {
				state = &toolCallState{
					id:         tc.ID,
					name:       tc.Function.Name,
					blockIndex: nextBlockIndex,
				}
				toolCalls[oaiIdx] = state
				nextBlockIndex++
				if err := writeAnthropicSSE(w, rc, &captured, "content_block_start",
					map[string]interface{}{
						"type":  "content_block_start",
						"index": state.blockIndex,
						"content_block": map[string]interface{}{
							"type":  "tool_use",
							"id":    state.id,
							"name":  state.name,
							"input": map[string]interface{}{},
						},
					}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, 0), err
				}
			}

			if tc.Function.Arguments != "" {
				if err := writeAnthropicSSE(w, rc, &captured, "content_block_delta",
					map[string]interface{}{
						"type":  "content_block_delta",
						"index": state.blockIndex,
						"delta": map[string]interface{}{
							"type":         "input_json_delta",
							"partial_json": tc.Function.Arguments,
						},
					}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, 0), err
				}
			}
		}
	}

	if sentMessageStart && !sentDone {
		slog.Warn("upstream OpenAI stream ended without [DONE] after message_start was sent")
	}

	return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, 0), scanner.Err()
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
