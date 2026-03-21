// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

// stream_responses.go contains Responses API SSE stream translation:
//   - Anthropic → Responses (AnthropicSSEToResponsesSSE)
//   - OpenAI → Responses (OpenAISSEToResponsesSSE)
//   - Responses → Anthropic (ResponsesSSEToAnthropicSSE)
//   - Responses → OpenAI (ResponsesSSEToOpenAISSE)

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
	"github.com/seckatie/glitchgate/internal/provider/openai"
	"github.com/seckatie/glitchgate/internal/sse"
)

// ---------------------------------------------------------------------------
// Anthropic → Responses SSE
// ---------------------------------------------------------------------------

// AnthropicSSEToResponsesSSE reads Anthropic SSE events from upstream, translates
// each to Responses API SSE events, and writes them to the client.
func AnthropicSSEToResponsesSSE(w http.ResponseWriter, upstream io.ReadCloser, model string) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()

	rc := http.NewResponseController(w)
	sse.WriteHeaders(w)

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
	textBlockSeen := false // once text starts, suppress late-arriving thinking blocks
	var thinkingText strings.Builder
	reasoningItemID := ""
	outputIndex := 0 // tracks the next output index for Responses API events
	var reasoningOutputItems []map[string]interface{}

	scanner := sse.NewScanner(upstream)
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
				if err := sse.WriteEvent(w, rc, &captured, "response.created", map[string]interface{}{
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
			var cbEvent anthropic.ContentBlockStartEvent
			if err := json.Unmarshal([]byte(data), &cbEvent); err != nil {
				continue
			}

			if cbEvent.ContentBlock.Type == "thinking" {
				// Suppress late-arriving thinking blocks after text has
				// started (e.g. Kimi-K2.5 interleaves thinking/text blocks).
				if textBlockSeen {
					inThinkingBlock = false
					continue
				}
				inThinkingBlock = true
				thinkingText.Reset()
				reasoningItemID = fmt.Sprintf("rs_%s_%d", strings.TrimPrefix(responseID, "resp_"), outputIndex)

				if err := sse.WriteEvent(w, rc, &captured, "response.output_item.added", map[string]interface{}{
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
			textBlockSeen = true

			if !sentOutputItemAdded {
				if err := sse.WriteEvent(w, rc, &captured, "response.output_item.added", map[string]interface{}{
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

			if !sentContentPartAdded {
				if err := sse.WriteEvent(w, rc, &captured, "response.content_part.added", map[string]interface{}{
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

			if inThinkingBlock && event.Delta.Type == "thinking_delta" && event.Delta.Thinking != "" {
				thinkingText.WriteString(event.Delta.Thinking)
				continue
			}

			if event.Delta.Type == "text_delta" && event.Delta.Text != "" {
				fullText.WriteString(event.Delta.Text)
				if err := sse.WriteEvent(w, rc, &captured, "response.output_text.delta", map[string]interface{}{
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
				thinkingSummary := thinkingText.String()
				if err := sse.WriteEvent(w, rc, &captured, "response.reasoning_summary_part.done", map[string]interface{}{
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

				if err := sse.WriteEvent(w, rc, &captured, "response.output_item.done", map[string]interface{}{
					"type":         "response.output_item.done",
					"output_index": outputIndex,
					"item":         reasoningItem,
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
				}

				reasoningOutputItems = append(reasoningOutputItems, reasoningItem)

				outputIndex++
				inThinkingBlock = false
				continue
			}

			finalText := fullText.String()
			if sentContentPartAdded {
				if err := sse.WriteEvent(w, rc, &captured, "response.output_text.done", map[string]interface{}{
					"type":          "response.output_text.done",
					"item_id":       messageID,
					"output_index":  outputIndex,
					"content_index": 0,
					"text":          finalText,
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
				}

				if err := sse.WriteEvent(w, rc, &captured, "response.content_part.done", map[string]interface{}{
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

			finalText := fullText.String()
			if err := sse.WriteEvent(w, rc, &captured, "response.output_item.done", map[string]interface{}{
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

			if cacheReadTokens > 0 {
				usage["input_tokens_details"] = map[string]interface{}{
					"cached_tokens": cacheReadTokens,
				}
			}

			if err := sse.WriteEvent(w, rc, &captured, "response.completed", map[string]interface{}{
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

// ---------------------------------------------------------------------------
// OpenAI → Responses SSE
// ---------------------------------------------------------------------------

// OpenAISSEToResponsesSSE reads OpenAI Chat Completions SSE events from upstream,
// translates each to Responses API SSE events, and writes them to the client.
func OpenAISSEToResponsesSSE(w http.ResponseWriter, upstream io.ReadCloser, model string) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()

	rc := http.NewResponseController(w)
	sse.WriteHeaders(w)

	var captured bytes.Buffer
	var inputTokens, outputTokens, cacheReadTokens, reasoningTokens int64
	var fullText strings.Builder
	messageID := ""
	responseID := ""
	createdAt := float64(time.Now().Unix())
	sentResponseCreated := false
	sentOutputItemAdded := false
	sentContentPartAdded := false

	// Reasoning block state.
	inReasoningBlock := false
	reasoningBlockClosed := false
	var reasoningText strings.Builder
	reasoningItemID := ""
	outputIndex := 0
	var reasoningOutputItems []map[string]interface{}

	scanner := sse.NewScanner(upstream)
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]

		if data == "[DONE]" {
			// Close reasoning block if still open.
			if inReasoningBlock && !reasoningBlockClosed {
				reasoningBlockClosed = true
				thinkingSummary := reasoningText.String()
				if err := sse.WriteEvent(w, rc, &captured, "response.reasoning_summary_part.done", map[string]interface{}{
					"type":          "response.reasoning_summary_part.done",
					"item_id":       reasoningItemID,
					"output_index":  outputIndex,
					"summary_index": 0,
					"part": map[string]interface{}{
						"type": "summary_text",
						"text": thinkingSummary,
					},
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
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
				if err := sse.WriteEvent(w, rc, &captured, "response.output_item.done", map[string]interface{}{
					"type":         "response.output_item.done",
					"output_index": outputIndex,
					"item":         reasoningItem,
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
				reasoningOutputItems = append(reasoningOutputItems, reasoningItem)
				outputIndex++
			}

			finalText := fullText.String()

			if sentContentPartAdded {
				if err := sse.WriteEvent(w, rc, &captured, "response.output_text.done", map[string]interface{}{
					"type":          "response.output_text.done",
					"item_id":       messageID,
					"output_index":  outputIndex,
					"content_index": 0,
					"text":          finalText,
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}

				if err := sse.WriteEvent(w, rc, &captured, "response.content_part.done", map[string]interface{}{
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
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
			}

			if sentOutputItemAdded {
				if err := sse.WriteEvent(w, rc, &captured, "response.output_item.done", map[string]interface{}{
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
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
			}

			// Build output array for response.completed.
			completedOutput := make([]interface{}, 0, len(reasoningOutputItems)+1)
			for _, ri := range reasoningOutputItems {
				completedOutput = append(completedOutput, ri)
			}
			completedOutput = append(completedOutput, map[string]interface{}{
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
			if err := sse.WriteEvent(w, rc, &captured, "response.completed", map[string]interface{}{
				"type": "response.completed",
				"response": map[string]interface{}{
					"id":         responseID,
					"object":     "response",
					"status":     "completed",
					"model":      model,
					"created_at": createdAt,
					"output":     completedOutput,
					"usage":      completedUsage,
				},
			}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}

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

		var chunk openai.ChatCompletionResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if chunk.ID != "" && responseID == "" {
			responseID = "resp_" + strings.TrimPrefix(chunk.ID, "chatcmpl-")
			messageID = "msg_" + strings.TrimPrefix(chunk.ID, "chatcmpl-")
			reasoningItemID = "rs_" + strings.TrimPrefix(chunk.ID, "chatcmpl-")
		}

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

		if !sentResponseCreated {
			createdAt = float64(chunk.Created)
			if err := sse.WriteEvent(w, rc, &captured, "response.created", map[string]interface{}{
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

		// Handle reasoning/thinking content deltas.
		thinkingText := delta.Reasoning
		if thinkingText == "" {
			thinkingText = delta.ReasoningContent
		}
		if thinkingText != "" && !reasoningBlockClosed {
			if !inReasoningBlock {
				inReasoningBlock = true
				if err := sse.WriteEvent(w, rc, &captured, "response.output_item.added", map[string]interface{}{
					"type":         "response.output_item.added",
					"output_index": outputIndex,
					"item": map[string]interface{}{
						"type":    "reasoning",
						"id":      reasoningItemID,
						"summary": []interface{}{},
						"status":  "in_progress",
					},
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
			}
			reasoningText.WriteString(thinkingText)
		}

		// Handle text content deltas.
		if text, ok := delta.Content.(string); ok && text != "" {
			// Close reasoning block before starting text content.
			if inReasoningBlock && !reasoningBlockClosed {
				reasoningBlockClosed = true
				thinkingSummary := reasoningText.String()
				if err := sse.WriteEvent(w, rc, &captured, "response.reasoning_summary_part.done", map[string]interface{}{
					"type":          "response.reasoning_summary_part.done",
					"item_id":       reasoningItemID,
					"output_index":  outputIndex,
					"summary_index": 0,
					"part": map[string]interface{}{
						"type": "summary_text",
						"text": thinkingSummary,
					},
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
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
				if err := sse.WriteEvent(w, rc, &captured, "response.output_item.done", map[string]interface{}{
					"type":         "response.output_item.done",
					"output_index": outputIndex,
					"item":         reasoningItem,
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
				reasoningOutputItems = append(reasoningOutputItems, reasoningItem)
				outputIndex++
			}

			if !sentOutputItemAdded {
				if err := sse.WriteEvent(w, rc, &captured, "response.output_item.added", map[string]interface{}{
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
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
				sentOutputItemAdded = true
			}

			if !sentContentPartAdded {
				if err := sse.WriteEvent(w, rc, &captured, "response.content_part.added", map[string]interface{}{
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
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
				sentContentPartAdded = true
			}

			fullText.WriteString(text)
			if err := sse.WriteEvent(w, rc, &captured, "response.output_text.delta", map[string]interface{}{
				"type":          "response.output_text.delta",
				"item_id":       messageID,
				"output_index":  outputIndex,
				"content_index": 0,
				"delta":         text,
			}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}
		}
	}

	// Estimate reasoning tokens from accumulated text when upstream
	// doesn't report them (e.g. Kimi-K2.5 sends no usage object).
	if reasoningTokens == 0 && reasoningText.Len() > 0 {
		reasoningTokens = int64(reasoningText.Len()+3) / 4
	}

	return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), scanner.Err()
}

// ---------------------------------------------------------------------------
// Responses → Anthropic SSE
// ---------------------------------------------------------------------------

// ResponsesSSEToAnthropicSSE reads Responses API SSE events from upstream,
// translates each to Anthropic SSE events, and writes them to the client.
func ResponsesSSEToAnthropicSSE(ctx context.Context, w http.ResponseWriter, upstream io.ReadCloser, model string) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()
	stop := sse.CloseOnCancel(ctx, upstream)
	defer stop()

	rc := http.NewResponseController(w)
	sse.WriteHeaders(w)

	var captured bytes.Buffer
	var inputTokens, outputTokens, cacheReadTokens int64
	sentMessageStart := false
	sentStop := false
	messageID := ""
	openBlocks := make(map[int]string)

	scanner := sse.NewScanner(upstream)
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
			slog.Warn("failed to parse Responses API SSE event type", "error", err, "data", data[:min(len(data), 256)]) //nolint:gosec // structured slog prevents log injection
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
				if err := sse.WriteEvent(w, rc, &captured, "message_start",
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
				if err := sse.WriteEvent(w, rc, &captured, "content_block_start",
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
				if err := sse.WriteEvent(w, rc, &captured, "content_block_start",
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
				if err := sse.WriteEvent(w, rc, &captured, "content_block_start",
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
				if err := sse.WriteEvent(w, rc, &captured, "content_block_delta",
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
				if err := sse.WriteEvent(w, rc, &captured, "content_block_start",
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
				if err := sse.WriteEvent(w, rc, &captured, "content_block_delta",
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
				if err := sse.WriteEvent(w, rc, &captured, "content_block_stop",
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
				if err := sse.WriteEvent(w, rc, &captured, "content_block_stop",
					map[string]interface{}{"type": "content_block_stop", "index": event.OutputIndex}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, 0, 0), err
				}
				delete(openBlocks, event.OutputIndex)
			}

		case "response.completed":
			var event struct {
				Response struct {
					Usage *struct {
						InputTokens        int64                      `json:"input_tokens"`
						OutputTokens       int64                      `json:"output_tokens"`
						InputTokensDetails *openai.InputTokensDetails `json:"input_tokens_details"`
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
			if err := sse.WriteEvent(w, rc, &captured, "message_delta",
				map[string]interface{}{
					"type":  "message_delta",
					"delta": map[string]interface{}{"stop_reason": stopReason},
					"usage": map[string]interface{}{"output_tokens": outputTokens},
				}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, 0), err
			}

			if err := sse.WriteEvent(w, rc, &captured, "message_stop",
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

// ---------------------------------------------------------------------------
// Responses → OpenAI SSE
// ---------------------------------------------------------------------------

// ResponsesSSEToOpenAISSE reads Responses API SSE events from upstream,
// translates each to Chat Completions SSE events, and writes them to the client.
func ResponsesSSEToOpenAISSE(ctx context.Context, w http.ResponseWriter, upstream io.ReadCloser, model string) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()
	stop := sse.CloseOnCancel(ctx, upstream)
	defer stop()

	rc := http.NewResponseController(w)
	sse.WriteHeaders(w)

	var captured bytes.Buffer
	var inputTokens, outputTokens, cacheReadTokens int64
	messageID := ""
	var created int64
	sentInitial := false
	sentStop := false

	scanner := sse.NewScanner(upstream)
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]
		if data == "[DONE]" {
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
			slog.Warn("failed to parse Responses API SSE event type", "error", err, "data", data[:min(len(data), 256)]) //nolint:gosec // structured slog prevents log injection
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

			if !sentInitial {
				chunk := buildChunk(messageID, model, created, &openai.ChatMessage{
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
				chunk := buildChunk(messageID, model, created, &openai.ChatMessage{
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
						InputTokens        int64                      `json:"input_tokens"`
						OutputTokens       int64                      `json:"output_tokens"`
						InputTokensDetails *openai.InputTokensDetails `json:"input_tokens_details"`
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
			chunk := buildChunk(messageID, model, created, &openai.ChatMessage{}, &finishReason)
			if err := writeSSEChunk(w, rc, &captured, chunk); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, 0), err
			}

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
