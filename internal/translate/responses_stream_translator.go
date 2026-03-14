// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"codeberg.org/kglitchy/glitchgate/internal/provider/anthropic"
)

// AnthropicSSEToResponsesSSE reads Anthropic SSE events from upstream, translates
// each to Responses API SSE events, and writes them to the client.
func AnthropicSSEToResponsesSSE(w http.ResponseWriter, upstream io.ReadCloser, model string) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()

	rc := http.NewResponseController(w)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var captured bytes.Buffer
	var inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64
	var fullText strings.Builder
	messageID := ""
	responseID := ""
	createdAt := float64(time.Now().Unix())
	sentResponseCreated := false
	sentOutputItemAdded := false
	sentContentPartAdded := false
	inThinkingBlock := false
	var thinkingText strings.Builder
	reasoningItemID := ""
	outputIndex := 0 // tracks the next output index for Responses API events
	var reasoningOutputItems []map[string]interface{}

	scanner := newSSEScanner(upstream)
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]

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
			messageID = "msg_" + event.Message.ID
			responseID = "resp_" + event.Message.ID
			if event.Message.Usage.InputTokens > 0 {
				inputTokens = event.Message.Usage.InputTokens
			}
			cacheCreationTokens = event.Message.Usage.CacheCreationInputTokens
			cacheReadTokens = event.Message.Usage.CacheReadInputTokens

			// Send response.created
			if !sentResponseCreated {
				if err := writeResponsesSSE(w, rc, &captured, "response.created", map[string]interface{}{
					"type": "response.created",
					"response": map[string]interface{}{
						"id":         responseID,
						"object":     "response",
						"status":     "in_progress",
						"model":      model,
						"created_at": createdAt,
						"output":     []interface{}{},
					},
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
				}
				sentResponseCreated = true
			}

		case "content_block_start":
			// Parse the block to check its type.
			var cbEvent anthropic.ContentBlockStartEvent
			if err := json.Unmarshal([]byte(data), &cbEvent); err != nil {
				continue
			}

			if cbEvent.ContentBlock.Type == "thinking" {
				inThinkingBlock = true
				thinkingText.Reset()
				reasoningItemID = fmt.Sprintf("rs_%s_%d", strings.TrimPrefix(responseID, "resp_"), outputIndex)

				// Emit response.output_item.added for reasoning item.
				if err := writeResponsesSSE(w, rc, &captured, "response.output_item.added", map[string]interface{}{
					"type":         "response.output_item.added",
					"output_index": outputIndex,
					"item": map[string]interface{}{
						"type":    "reasoning",
						"id":      reasoningItemID,
						"summary": []interface{}{},
						"status":  "in_progress",
					},
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
				}
				continue
			}

			inThinkingBlock = false

			// Emit output_item.added for the message item (deferred until first text block).
			if !sentOutputItemAdded {
				if err := writeResponsesSSE(w, rc, &captured, "response.output_item.added", map[string]interface{}{
					"type":         "response.output_item.added",
					"output_index": outputIndex,
					"item": map[string]interface{}{
						"type":    "message",
						"id":      messageID,
						"role":    "assistant",
						"content": []interface{}{},
						"status":  "in_progress",
					},
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
				}
				sentOutputItemAdded = true
			}

			// Send response.content_part.added for text blocks.
			if !sentContentPartAdded {
				if err := writeResponsesSSE(w, rc, &captured, "response.content_part.added", map[string]interface{}{
					"type":          "response.content_part.added",
					"item_id":       messageID,
					"output_index":  outputIndex,
					"content_index": 0,
					"part": map[string]interface{}{
						"type":        "output_text",
						"text":        "",
						"annotations": []interface{}{},
					},
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
				}
				sentContentPartAdded = true
			}

		case "content_block_delta":
			var event anthropic.ContentBlockDeltaEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}

			// Accumulate thinking text but don't forward as output_text.
			if inThinkingBlock && event.Delta.Type == "thinking_delta" && event.Delta.Thinking != "" {
				thinkingText.WriteString(event.Delta.Thinking)
				continue
			}

			if event.Delta.Type == "text_delta" && event.Delta.Text != "" {
				fullText.WriteString(event.Delta.Text)
				if err := writeResponsesSSE(w, rc, &captured, "response.output_text.delta", map[string]interface{}{
					"type":          "response.output_text.delta",
					"item_id":       messageID,
					"output_index":  outputIndex,
					"content_index": 0,
					"delta":         event.Delta.Text,
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
				}
			}

		case "content_block_stop":
			if inThinkingBlock {
				// Emit reasoning summary done + output_item.done for the thinking block.
				thinkingSummary := thinkingText.String()
				if err := writeResponsesSSE(w, rc, &captured, "response.reasoning_summary_part.done", map[string]interface{}{
					"type":          "response.reasoning_summary_part.done",
					"item_id":       reasoningItemID,
					"output_index":  outputIndex,
					"summary_index": 0,
					"part": map[string]interface{}{
						"type": "summary_text",
						"text": thinkingSummary,
					},
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
				}

				reasoningItem := map[string]interface{}{
					"type": "reasoning",
					"id":   reasoningItemID,
					"summary": []interface{}{
						map[string]interface{}{
							"type": "summary_text",
							"text": thinkingSummary,
						},
					},
					"status": "completed",
				}

				if err := writeResponsesSSE(w, rc, &captured, "response.output_item.done", map[string]interface{}{
					"type":         "response.output_item.done",
					"output_index": outputIndex,
					"item":         reasoningItem,
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
				}

				// Save for final envelope.
				reasoningOutputItems = append(reasoningOutputItems, reasoningItem)

				outputIndex++
				inThinkingBlock = false
				continue
			}

			// Send response.output_text.done and response.content_part.done for text blocks.
			finalText := fullText.String()
			if sentContentPartAdded {
				if err := writeResponsesSSE(w, rc, &captured, "response.output_text.done", map[string]interface{}{
					"type":          "response.output_text.done",
					"item_id":       messageID,
					"output_index":  outputIndex,
					"content_index": 0,
					"text":          finalText,
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
				}

				if err := writeResponsesSSE(w, rc, &captured, "response.content_part.done", map[string]interface{}{
					"type":          "response.content_part.done",
					"item_id":       messageID,
					"output_index":  outputIndex,
					"content_index": 0,
					"part": map[string]interface{}{
						"type":        "output_text",
						"text":        finalText,
						"annotations": []interface{}{},
					},
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
				}
			}

		case "message_delta":
			var event anthropic.MessageDeltaEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}
			if event.Usage != nil {
				outputTokens = event.Usage.OutputTokens
			}

			// Send response.output_item.done for the message item.
			finalText := fullText.String()
			if err := writeResponsesSSE(w, rc, &captured, "response.output_item.done", map[string]interface{}{
				"type":         "response.output_item.done",
				"output_index": outputIndex,
				"item": map[string]interface{}{
					"type": "message",
					"id":   messageID,
					"role": "assistant",
					"content": []interface{}{
						map[string]interface{}{
							"type":        "output_text",
							"text":        finalText,
							"annotations": []interface{}{},
						},
					},
					"status": "completed",
				},
			}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
			}

		case "message_stop":
			// Build output array with reasoning items followed by message.
			totalTokens := inputTokens + outputTokens
			finalText := fullText.String()

			outputItems := make([]interface{}, 0, len(reasoningOutputItems)+1)
			for _, ri := range reasoningOutputItems {
				outputItems = append(outputItems, ri)
			}
			outputItems = append(outputItems, map[string]interface{}{
				"type": "message",
				"id":   messageID,
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{
						"type":        "output_text",
						"text":        finalText,
						"annotations": []interface{}{},
					},
				},
				"status": "completed",
			})

			usage := map[string]interface{}{
				"input_tokens":  inputTokens,
				"output_tokens": outputTokens,
				"total_tokens":  totalTokens,
			}

			// Add input_tokens_details if caching occurred.
			if cacheReadTokens > 0 {
				usage["input_tokens_details"] = map[string]interface{}{
					"cached_tokens": cacheReadTokens,
				}
			}

			if err := writeResponsesSSE(w, rc, &captured, "response.completed", map[string]interface{}{
				"type": "response.completed",
				"response": map[string]interface{}{
					"id":         responseID,
					"object":     "response",
					"status":     "completed",
					"model":      model,
					"created_at": createdAt,
					"output":     outputItems,
					"usage":      usage,
				},
			}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
			}

			// Write [DONE] sentinel.
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

	return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), scanner.Err()
}

// OpenAISSEToResponsesSSE reads OpenAI Chat Completions SSE events from upstream,
// translates each to Responses API SSE events, and writes them to the client.
func OpenAISSEToResponsesSSE(w http.ResponseWriter, upstream io.ReadCloser, model string) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()

	rc := http.NewResponseController(w)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var captured bytes.Buffer
	var inputTokens, outputTokens, cacheReadTokens, reasoningTokens int64
	var fullText strings.Builder
	messageID := ""
	responseID := ""
	createdAt := float64(time.Now().Unix())
	sentResponseCreated := false
	sentOutputItemAdded := false
	sentContentPartAdded := false

	scanner := newSSEScanner(upstream)
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]

		if data == "[DONE]" {
			// Send completion events.
			finalText := fullText.String()

			// response.output_text.done
			if sentContentPartAdded {
				if err := writeResponsesSSE(w, rc, &captured, "response.output_text.done", map[string]interface{}{
					"type":          "response.output_text.done",
					"item_id":       messageID,
					"output_index":  0,
					"content_index": 0,
					"text":          finalText,
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}

				if err := writeResponsesSSE(w, rc, &captured, "response.content_part.done", map[string]interface{}{
					"type":          "response.content_part.done",
					"item_id":       messageID,
					"output_index":  0,
					"content_index": 0,
					"part": map[string]interface{}{
						"type":        "output_text",
						"text":        finalText,
						"annotations": []interface{}{},
					},
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
			}

			// response.output_item.done
			if sentOutputItemAdded {
				if err := writeResponsesSSE(w, rc, &captured, "response.output_item.done", map[string]interface{}{
					"type":         "response.output_item.done",
					"output_index": 0,
					"item": map[string]interface{}{
						"type": "message",
						"id":   messageID,
						"role": "assistant",
						"content": []interface{}{
							map[string]interface{}{
								"type":        "output_text",
								"text":        finalText,
								"annotations": []interface{}{},
							},
						},
						"status": "completed",
					},
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
			}

			// response.completed
			totalTokens := inputTokens + outputTokens
			completedUsage := map[string]interface{}{
				"input_tokens":  inputTokens,
				"output_tokens": outputTokens,
				"total_tokens":  totalTokens,
			}
			if cacheReadTokens > 0 {
				completedUsage["input_tokens_details"] = map[string]interface{}{
					"cached_tokens": cacheReadTokens,
				}
			}
			if err := writeResponsesSSE(w, rc, &captured, "response.completed", map[string]interface{}{
				"type": "response.completed",
				"response": map[string]interface{}{
					"id":         responseID,
					"object":     "response",
					"status":     "completed",
					"model":      model,
					"created_at": createdAt,
					"output": []interface{}{
						map[string]interface{}{
							"type": "message",
							"id":   messageID,
							"role": "assistant",
							"content": []interface{}{
								map[string]interface{}{
									"type":        "output_text",
									"text":        finalText,
									"annotations": []interface{}{},
								},
							},
							"status": "completed",
						},
					},
					"usage": completedUsage,
				},
			}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}

			// Write [DONE] sentinel.
			done := "data: [DONE]\n\n"
			captured.WriteString(done)
			if _, err := w.Write([]byte(done)); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}
			if err := rc.Flush(); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}
			continue
		}

		// Parse the OpenAI chunk.
		var chunk ChatCompletionResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		// Set IDs from first chunk.
		if chunk.ID != "" && responseID == "" {
			responseID = "resp_" + strings.TrimPrefix(chunk.ID, "chatcmpl-")
			messageID = "msg_" + strings.TrimPrefix(chunk.ID, "chatcmpl-")
		}

		// Extract usage from final chunk.
		if chunk.Usage != nil {
			inputTokens = chunk.Usage.PromptTokens
			outputTokens = chunk.Usage.CompletionTokens
			if chunk.Usage.PromptTokensDetails != nil {
				cacheReadTokens = chunk.Usage.PromptTokensDetails.CachedTokens
			}
			if chunk.Usage.CompletionTokensDetails != nil {
				reasoningTokens = chunk.Usage.CompletionTokensDetails.ReasoningTokens
			}
		}

		// Send response.created on first chunk.
		if !sentResponseCreated {
			createdAt = float64(chunk.Created)
			if err := writeResponsesSSE(w, rc, &captured, "response.created", map[string]interface{}{
				"type": "response.created",
				"response": map[string]interface{}{
					"id":         responseID,
					"object":     "response",
					"status":     "in_progress",
					"model":      model,
					"created_at": createdAt,
					"output":     []interface{}{},
				},
			}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}
			sentResponseCreated = true
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]
		delta := choice.Delta
		if delta == nil {
			continue
		}

		// Send output_item.added and content_part.added on first content.
		if !sentOutputItemAdded {
			if err := writeResponsesSSE(w, rc, &captured, "response.output_item.added", map[string]interface{}{
				"type":         "response.output_item.added",
				"output_index": 0,
				"item": map[string]interface{}{
					"type":    "message",
					"id":      messageID,
					"role":    "assistant",
					"content": []interface{}{},
					"status":  "in_progress",
				},
			}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}
			sentOutputItemAdded = true
		}

		// Handle text content deltas.
		if text, ok := delta.Content.(string); ok && text != "" {
			if !sentContentPartAdded {
				if err := writeResponsesSSE(w, rc, &captured, "response.content_part.added", map[string]interface{}{
					"type":          "response.content_part.added",
					"item_id":       messageID,
					"output_index":  0,
					"content_index": 0,
					"part": map[string]interface{}{
						"type":        "output_text",
						"text":        "",
						"annotations": []interface{}{},
					},
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
				sentContentPartAdded = true
			}

			fullText.WriteString(text)
			if err := writeResponsesSSE(w, rc, &captured, "response.output_text.delta", map[string]interface{}{
				"type":          "response.output_text.delta",
				"item_id":       messageID,
				"output_index":  0,
				"content_index": 0,
				"delta":         text,
			}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}
		}
	}

	return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), scanner.Err()
}

// writeResponsesSSE writes a single Responses API SSE event to the client.
func writeResponsesSSE(w http.ResponseWriter, rc *http.ResponseController, captured *bytes.Buffer, event string, data interface{}) error {
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
