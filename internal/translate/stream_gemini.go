// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

// stream_gemini.go contains Gemini SSE stream translation:
//   - Gemini → Anthropic (GeminiSSEToAnthropicSSE)
//   - Gemini → OpenAI (GeminiSSEToOpenAISSE)
//   - Gemini → Responses (GeminiSSEToResponsesSSE)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/seckatie/glitchgate/internal/provider/gemini"
	"github.com/seckatie/glitchgate/internal/provider/openai"
	"github.com/seckatie/glitchgate/internal/sse"
)

// ---------------------------------------------------------------------------
// Gemini → Anthropic SSE
// ---------------------------------------------------------------------------

// GeminiSSEToAnthropicSSE reads Gemini streamGenerateContent SSE events from
// upstream, translates each to Anthropic-format SSE events, and writes them to
// the client.
func GeminiSSEToAnthropicSSE(w http.ResponseWriter, upstream io.ReadCloser, model string) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()

	rc := http.NewResponseController(w)
	sse.WriteHeaders(w)

	var captured bytes.Buffer
	var inputTokens, outputTokens, cacheReadTokens, reasoningTokens int64
	sentMessageStart := false
	textBlockIndex := -1
	nextBlockIndex := 0
	finishReason := "end_turn"

	type toolState struct {
		id    string
		index int
	}
	toolCalls := make(map[string]*toolState) // fc.Name → state
	toolUseIdx := 0

	scanner := sse.NewScanner(upstream)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := line[6:]

		var gr gemini.Response
		if err := json.Unmarshal([]byte(data), &gr); err != nil {
			slog.Warn("failed to parse Gemini SSE event", "error", err, "data", data[:min(len(data), 256)]) //nolint:gosec // structured slog prevents log injection
			continue
		}

		if gr.UsageMetadata != nil {
			inputTokens, outputTokens, cacheReadTokens, reasoningTokens = gemini.UsageTotals(gr.UsageMetadata)
		}

		if !sentMessageStart {
			if err := sse.WriteEvent(w, rc, &captured, "message_start", map[string]interface{}{
				"type": "message_start",
				"message": map[string]interface{}{
					"id":      "msg_gemini",
					"type":    "message",
					"role":    "assistant",
					"content": []interface{}{},
					"model":   model,
					"usage": map[string]interface{}{
						"input_tokens":                inputTokens,
						"output_tokens":               0,
						"cache_creation_input_tokens": 0,
						"cache_read_input_tokens":     cacheReadTokens,
					},
				},
			}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}
			sentMessageStart = true
		}

		if len(gr.Candidates) == 0 {
			continue
		}
		cand := gr.Candidates[0]

		if cand.FinishReason != "" {
			hasToolUse := len(toolCalls) > 0
			switch cand.FinishReason {
			case "MAX_TOKENS":
				finishReason = "max_tokens"
			case "STOP":
				if hasToolUse {
					finishReason = "tool_use"
				} else {
					finishReason = "end_turn"
				}
			default:
				finishReason = "end_turn"
			}
		}

		if cand.Content == nil {
			continue
		}

		for _, part := range cand.Content.Parts {
			switch {
			case part.Text != "":
				if textBlockIndex < 0 {
					textBlockIndex = nextBlockIndex
					nextBlockIndex++
					if err := sse.WriteEvent(w, rc, &captured, "content_block_start", map[string]interface{}{
						"type":          "content_block_start",
						"index":         textBlockIndex,
						"content_block": map[string]interface{}{"type": "text", "text": ""},
					}); err != nil {
						return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
					}
				}
				if err := sse.WriteEvent(w, rc, &captured, "content_block_delta", map[string]interface{}{
					"type":  "content_block_delta",
					"index": textBlockIndex,
					"delta": map[string]interface{}{"type": "text_delta", "text": part.Text},
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}

			case part.FunctionCall != nil:
				fc := part.FunctionCall
				state, exists := toolCalls[fc.Name]
				if !exists {
					toolID := gemini.EncodeToolCallID(fmt.Sprintf("toolu_%06d", toolUseIdx), part.ThoughtSignature)
					toolUseIdx++
					state = &toolState{id: toolID, index: nextBlockIndex}
					toolCalls[fc.Name] = state
					nextBlockIndex++
					if err := sse.WriteEvent(w, rc, &captured, "content_block_start", map[string]interface{}{
						"type":  "content_block_start",
						"index": state.index,
						"content_block": map[string]interface{}{
							"type":  "tool_use",
							"id":    state.id,
							"name":  fc.Name,
							"input": map[string]interface{}{},
						},
					}); err != nil {
						return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
					}
				}
				if fc.Args != nil {
					argsJSON, err := json.Marshal(fc.Args)
					if err == nil && len(argsJSON) > 0 && string(argsJSON) != "null" {
						if err := sse.WriteEvent(w, rc, &captured, "content_block_delta", map[string]interface{}{
							"type":  "content_block_delta",
							"index": state.index,
							"delta": map[string]interface{}{
								"type":         "input_json_delta",
								"partial_json": string(argsJSON),
							},
						}); err != nil {
							return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
						}
					}
				}
			}
		}
	}

	if sentMessageStart {
		if textBlockIndex >= 0 {
			if err := sse.WriteEvent(w, rc, &captured, "content_block_stop", map[string]interface{}{
				"type":  "content_block_stop",
				"index": textBlockIndex,
			}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}
		}

		type tcEntry struct {
			name  string
			index int
		}
		var entries []tcEntry
		for name, s := range toolCalls {
			entries = append(entries, tcEntry{name, s.index})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].index < entries[j].index })
		for _, e := range entries {
			if err := sse.WriteEvent(w, rc, &captured, "content_block_stop", map[string]interface{}{
				"type":  "content_block_stop",
				"index": e.index,
			}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}
		}

		if len(toolCalls) > 0 && finishReason == "end_turn" {
			finishReason = "tool_use"
		}

		if err := sse.WriteEvent(w, rc, &captured, "message_delta", map[string]interface{}{
			"type":  "message_delta",
			"delta": map[string]interface{}{"stop_reason": finishReason},
			"usage": map[string]interface{}{"output_tokens": outputTokens},
		}); err != nil {
			return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
		}

		if err := sse.WriteEvent(w, rc, &captured, "message_stop", map[string]interface{}{
			"type": "message_stop",
		}); err != nil {
			return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
		}
	}

	return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), scanner.Err()
}

// ---------------------------------------------------------------------------
// Gemini → OpenAI SSE
// ---------------------------------------------------------------------------

// GeminiSSEToOpenAISSE reads Gemini SSE events from upstream and emits
// OpenAI-compatible streaming chunks to the client.
func GeminiSSEToOpenAISSE(w http.ResponseWriter, upstream io.ReadCloser, model string) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()

	rc := http.NewResponseController(w)
	sse.WriteHeaders(w)

	var captured bytes.Buffer
	var inputTokens, outputTokens, cacheReadTokens, reasoningTokens int64
	created := time.Now().Unix()
	const msgID = "chatcmpl-gemini"
	sentInitial := false
	finishReason := "stop"
	toolUseIdx := 0
	toolIndexByName := make(map[string]int)

	scanner := sse.NewScanner(upstream)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := line[6:]

		var gr gemini.Response
		if err := json.Unmarshal([]byte(data), &gr); err != nil {
			slog.Warn("failed to parse Gemini SSE event (OpenAI path)", "error", err, "data", data[:min(len(data), 256)]) //nolint:gosec // structured slog prevents log injection
			continue
		}

		if gr.UsageMetadata != nil {
			inputTokens, outputTokens, cacheReadTokens, reasoningTokens = gemini.UsageTotals(gr.UsageMetadata)
		}

		if !sentInitial {
			chunk := buildChunk(msgID, model, created, &openai.ChatMessage{Role: "assistant", Content: ""}, nil)
			if err := writeSSEChunk(w, rc, &captured, chunk); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}
			sentInitial = true
		}

		if len(gr.Candidates) == 0 {
			continue
		}
		cand := gr.Candidates[0]

		if cand.FinishReason != "" {
			hasTC := len(toolIndexByName) > 0
			switch cand.FinishReason {
			case "MAX_TOKENS":
				finishReason = "length"
			case "STOP":
				if hasTC {
					finishReason = "tool_calls"
				} else {
					finishReason = "stop"
				}
			default:
				finishReason = "stop"
			}
		}

		if cand.Content == nil {
			continue
		}

		for _, part := range cand.Content.Parts {
			switch {
			case part.Text != "":
				chunk := buildChunk(msgID, model, created, &openai.ChatMessage{Content: part.Text}, nil)
				if err := writeSSEChunk(w, rc, &captured, chunk); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}

			case part.FunctionCall != nil:
				fc := part.FunctionCall
				tcIdx, exists := toolIndexByName[fc.Name]
				if !exists {
					tcIdx = toolUseIdx
					toolIndexByName[fc.Name] = tcIdx
					toolUseIdx++
				}
				argsStr := ""
				if fc.Args != nil {
					argsJSON, err := json.Marshal(fc.Args)
					if err == nil {
						argsStr = string(argsJSON)
					}
				}
				tcIdx32 := tcIdx
				callID := gemini.EncodeToolCallID(fmt.Sprintf("call_%06d", tcIdx), part.ThoughtSignature)
				toolChunk := &openai.ChatCompletionResponse{
					ID:      msgID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   model,
					Choices: []openai.Choice{{
						Index: 0,
						Delta: &openai.ChatMessage{
							ToolCalls: []openai.ToolCall{{
								Index:    &tcIdx32,
								ID:       callID,
								Type:     "function",
								Function: openai.FunctionCall{Name: fc.Name, Arguments: argsStr},
							}},
						},
					}},
				}
				if err := writeSSEChunk(w, rc, &captured, toolChunk); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
			}
		}
	}

	if sentInitial {
		if len(toolIndexByName) > 0 && finishReason == "stop" {
			finishReason = "tool_calls"
		}

		finalChunk := buildChunk(msgID, model, created, &openai.ChatMessage{}, &finishReason)
		if err := writeSSEChunk(w, rc, &captured, finalChunk); err != nil {
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
	}

	return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), scanner.Err()
}

// ---------------------------------------------------------------------------
// Gemini → Responses SSE
// ---------------------------------------------------------------------------

// GeminiSSEToResponsesSSE reads Gemini SSE events from upstream and emits
// Responses API-format streaming events to the client.
func GeminiSSEToResponsesSSE(w http.ResponseWriter, upstream io.ReadCloser, model string) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()

	rc := http.NewResponseController(w)
	sse.WriteHeaders(w)

	var captured bytes.Buffer
	var inputTokens, outputTokens, cacheReadTokens, reasoningTokens int64
	const respID = "resp_gemini"
	const itemID = "item_gemini"
	sentCreated := false
	textStarted := false
	var textAccum strings.Builder
	finishReason := "end_turn"
	toolUseIdx := 0

	writeEvent := func(eventType string, payload interface{}) error {
		return sse.WriteEvent(w, rc, &captured, eventType, payload)
	}

	scanner := sse.NewScanner(upstream)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := line[6:]

		var gr gemini.Response
		if err := json.Unmarshal([]byte(data), &gr); err != nil {
			slog.Warn("failed to parse Gemini SSE event (Responses path)", "error", err, "data", data[:min(len(data), 256)]) //nolint:gosec // structured slog prevents log injection
			continue
		}

		if gr.UsageMetadata != nil {
			inputTokens, outputTokens, cacheReadTokens, reasoningTokens = gemini.UsageTotals(gr.UsageMetadata)
		}

		if !sentCreated {
			if err := writeEvent("response.created", map[string]interface{}{
				"type": "response.created",
				"response": map[string]interface{}{
					"id":     respID,
					"object": "response",
					"status": "in_progress",
					"model":  model,
				},
			}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}
			if err := writeEvent("response.output_item.added", map[string]interface{}{
				"type":         "response.output_item.added",
				"output_index": 0,
				"item": map[string]interface{}{
					"id":      itemID,
					"type":    "message",
					"status":  "in_progress",
					"role":    "assistant",
					"content": []interface{}{},
				},
			}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}
			sentCreated = true
		}

		if len(gr.Candidates) == 0 {
			continue
		}
		cand := gr.Candidates[0]

		if cand.FinishReason != "" {
			switch cand.FinishReason {
			case "MAX_TOKENS":
				finishReason = "max_tokens"
			default:
				finishReason = "end_turn"
			}
		}

		if cand.Content == nil {
			continue
		}

		for _, part := range cand.Content.Parts {
			switch {
			case part.Text != "":
				if !textStarted {
					if err := writeEvent("response.content_part.added", map[string]interface{}{
						"type":          "response.content_part.added",
						"item_id":       itemID,
						"output_index":  0,
						"content_index": 0,
						"part":          map[string]interface{}{"type": "output_text", "text": ""},
					}); err != nil {
						return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
					}
					textStarted = true
				}
				textAccum.WriteString(part.Text)
				if err := writeEvent("response.output_text.delta", map[string]interface{}{
					"type":          "response.output_text.delta",
					"item_id":       itemID,
					"output_index":  0,
					"content_index": 0,
					"delta":         part.Text,
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}

			case part.FunctionCall != nil:
				fc := part.FunctionCall
				callID := gemini.EncodeToolCallID(fmt.Sprintf("call_%06d", toolUseIdx), part.ThoughtSignature)
				toolUseIdx++
				argsStr := ""
				if fc.Args != nil {
					argsJSON, _ := json.Marshal(fc.Args)
					argsStr = string(argsJSON)
				}
				if err := writeEvent("response.output_item.added", map[string]interface{}{
					"type":         "response.output_item.added",
					"output_index": toolUseIdx,
					"item": map[string]interface{}{
						"type":      "function_call",
						"id":        callID,
						"call_id":   callID,
						"name":      fc.Name,
						"arguments": argsStr,
						"status":    "completed",
					},
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
			}
		}
	}

	if sentCreated {
		if textStarted {
			finalText := textAccum.String()
			if err := writeEvent("response.output_text.done", map[string]interface{}{
				"type":          "response.output_text.done",
				"item_id":       itemID,
				"output_index":  0,
				"content_index": 0,
				"text":          finalText,
			}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}
			if err := writeEvent("response.content_part.done", map[string]interface{}{
				"type":          "response.content_part.done",
				"item_id":       itemID,
				"output_index":  0,
				"content_index": 0,
				"part":          map[string]interface{}{"type": "output_text", "text": finalText},
			}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}
		}

		if err := writeEvent("response.output_item.done", map[string]interface{}{
			"type":         "response.output_item.done",
			"output_index": 0,
			"item": map[string]interface{}{
				"id":     itemID,
				"type":   "message",
				"status": "completed",
				"role":   "assistant",
			},
		}); err != nil {
			return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
		}

		usage := map[string]interface{}{
			"input_tokens":  inputTokens + cacheReadTokens,
			"output_tokens": outputTokens,
			"total_tokens":  inputTokens + cacheReadTokens + outputTokens,
		}
		if cacheReadTokens > 0 {
			usage["input_tokens_details"] = map[string]interface{}{
				"cached_tokens": cacheReadTokens,
			}
		}
		if reasoningTokens > 0 {
			usage["output_tokens_details"] = map[string]interface{}{
				"reasoning_tokens": reasoningTokens,
			}
		}
		if err := writeEvent("response.completed", map[string]interface{}{
			"type": "response.completed",
			"response": map[string]interface{}{
				"id":          respID,
				"object":      "response",
				"status":      "completed",
				"model":       model,
				"usage":       usage,
				"stop_reason": finishReason,
			},
		}); err != nil {
			return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
		}
	}

	return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), scanner.Err()
}
