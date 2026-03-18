// SPDX-License-Identifier: AGPL-3.0-or-later

// streaming.go consolidates all SSE stream translation functions:
//   - Anthropic → OpenAI (SSEStream)
//   - OpenAI → Anthropic (ReverseSSEStream)
//   - Anthropic → Responses (AnthropicSSEToResponsesSSE)
//   - OpenAI → Responses (OpenAISSEToResponsesSSE)
//   - Responses → Anthropic (ResponsesSSEToAnthropicSSE)
//   - Responses → OpenAI (ResponsesSSEToOpenAISSE)
//   - Gemini → Anthropic (GeminiSSEToAnthropicSSE)
//   - Gemini → OpenAI (GeminiSSEToOpenAISSE)
//   - Gemini → Responses (GeminiSSEToResponsesSSE)
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
	"time"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
	"github.com/seckatie/glitchgate/internal/provider/gemini"
	"github.com/seckatie/glitchgate/internal/provider/openai"
	"github.com/seckatie/glitchgate/internal/sse"
)

// StreamResult is a type alias for sse.StreamResult, preserving backward
// compatibility for callers that reference translate.StreamResult.
type StreamResult = sse.StreamResult

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// writeChunk is a helper that builds a chunk, writes it to the client,
// and returns an error (with partial result) if writing fails.
func writeChunk(w http.ResponseWriter, rc *http.ResponseController, captured *bytes.Buffer, id, model string, created int64, delta *openai.ChatMessage, finishReason *string, toolCalls map[int]openai.ToolCall, toolCallOrder []int) error {
	var chunk *openai.ChatCompletionResponse
	if len(toolCalls) > 0 {
		chunk = buildChunkWithToolCalls(id, model, created, delta, finishReason, toolCalls, toolCallOrder)
	} else {
		chunk = buildChunk(id, model, created, delta, finishReason)
	}
	return writeSSEChunk(w, rc, captured, chunk)
}

// buildChunk creates a openai.ChatCompletionResponse formatted as a streaming chunk.
func buildChunk(id, model string, created int64, delta *openai.ChatMessage, finishReason *string) *openai.ChatCompletionResponse {
	return &openai.ChatCompletionResponse{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []openai.Choice{
			{
				Index:        0,
				Delta:        delta,
				FinishReason: finishReason,
			},
		},
	}
}

// buildChunkWithToolCalls creates a openai.ChatCompletionResponse with tool calls.
func buildChunkWithToolCalls(id, model string, created int64, delta *openai.ChatMessage, finishReason *string, toolCalls map[int]openai.ToolCall, order []int) *openai.ChatCompletionResponse {
	// Build tool calls in order.
	tcSlice := make([]openai.ToolCall, 0, len(toolCalls))
	for _, idx := range order {
		if tc, ok := toolCalls[idx]; ok {
			tcSlice = append(tcSlice, tc)
		}
	}
	// Also include any tool calls not in order (shouldn't happen but be safe).
	for idx, tc := range toolCalls {
		found := false
		for _, o := range order {
			if o == idx {
				found = true
				break
			}
		}
		if !found {
			tcSlice = append(tcSlice, tc)
		}
	}

	delta.ToolCalls = tcSlice
	return &openai.ChatCompletionResponse{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []openai.Choice{
			{
				Index:        0,
				Delta:        delta,
				FinishReason: finishReason,
			},
		},
	}
}

// writeToolCallStartChunk emits the initial chunk for a new tool call with only
// index, ID, type, and name - not arguments.
func writeToolCallStartChunk(w http.ResponseWriter, rc *http.ResponseController, captured *bytes.Buffer, id, model string, created int64, toolCallIndex int, tcID, tcName string) error {
	delta := &openai.ChatMessage{
		Role:    "assistant",
		Content: "",
		ToolCalls: []openai.ToolCall{
			{
				ID:   tcID,
				Type: "function",
				Function: openai.FunctionCall{
					Name:      tcName,
					Arguments: "",
				},
				Index: &toolCallIndex,
			},
		},
	}

	chunk := &openai.ChatCompletionResponse{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []openai.Choice{
			{
				Index:        0,
				Delta:        delta,
				FinishReason: nil,
			},
		},
	}

	data, err := json.Marshal(chunk)
	if err != nil {
		return fmt.Errorf("marshalling tool call start chunk: %w", err)
	}

	line := fmt.Sprintf("data: %s\n\n", data)
	captured.WriteString(line)

	if _, err := w.Write([]byte(line)); err != nil {
		return err
	}
	return rc.Flush()
}

// writeToolCallDeltaChunk emits a chunk with only the incremental tool call arguments delta.
func writeToolCallDeltaChunk(w http.ResponseWriter, rc *http.ResponseController, captured *bytes.Buffer, id, model string, created int64, toolCallIndex int, newArgs string) error {
	delta := &openai.ChatMessage{
		Role:    "assistant",
		Content: "",
		ToolCalls: []openai.ToolCall{
			{
				ID:   "",
				Type: "function",
				Function: openai.FunctionCall{
					Name:      "",
					Arguments: newArgs, // Only the delta, not accumulated
				},
				Index: &toolCallIndex,
			},
		},
	}

	chunk := &openai.ChatCompletionResponse{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []openai.Choice{
			{
				Index:        0,
				Delta:        delta,
				FinishReason: nil,
			},
		},
	}

	data, err := json.Marshal(chunk)
	if err != nil {
		return fmt.Errorf("marshalling tool call delta chunk: %w", err)
	}

	line := fmt.Sprintf("data: %s\n\n", data)
	captured.WriteString(line)

	if _, err := w.Write([]byte(line)); err != nil {
		return err
	}
	return rc.Flush()
}

// writeSSEChunk serializes a chunk as an SSE data line and flushes it to the client.
func writeSSEChunk(w http.ResponseWriter, rc *http.ResponseController, captured *bytes.Buffer, chunk *openai.ChatCompletionResponse) error {
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
func buildResult(captured *bytes.Buffer, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, reasoningTokens int64) *sse.StreamResult {
	return sse.BuildResult(captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, reasoningTokens)
}

// ---------------------------------------------------------------------------
// Anthropic → OpenAI SSE (SSEStream)
// ---------------------------------------------------------------------------

// SSEStream reads Anthropic SSE events from upstream, translates
// each to OpenAI-compatible SSE chunks, and writes them to the client.
// It returns a StreamResult with the captured output and token usage.
func SSEStream(w http.ResponseWriter, upstream io.ReadCloser, model string) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()

	rc := http.NewResponseController(w)
	sse.WriteHeaders(w)

	var captured bytes.Buffer
	var inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64
	created := time.Now().Unix()
	messageID := ""
	sentInitial := false
	sentStop := false
	thinkingBlockIndices := make(map[int]bool)

	// Track active tool calls by index for streaming tool call support.
	toolCalls := make(map[int]openai.ToolCall)
	toolCallOrder := []int{} // track order of tool calls
	// Map Anthropic content block indices to sequential tool call indices
	contentBlockToToolCallIndex := make(map[int]int)

	scanner := sse.NewScanner(upstream)
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
				delta := &openai.ChatMessage{
					Role:    "assistant",
					Content: "",
				}
				if err := writeChunk(w, rc, &captured, messageID, model, created, delta, nil, nil, nil); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
				}
				sentInitial = true
			}

		case "content_block_start":
			var cbEvent anthropic.ContentBlockStartEvent
			if err := json.Unmarshal([]byte(data), &cbEvent); err != nil {
				slog.Warn("failed to parse content_block_start event", "error", err)
			} else {
				slog.Debug("content_block_start", "index", cbEvent.Index, "type", cbEvent.ContentBlock.Type)
				if cbEvent.ContentBlock.Type == "thinking" {
					thinkingBlockIndices[cbEvent.Index] = true
					delta := &openai.ChatMessage{
						Role:      "assistant",
						Content:   "",
						Reasoning: "",
					}
					if err := writeChunk(w, rc, &captured, messageID, model, created, delta, nil, nil, nil); err != nil {
						return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
					}
				} else if cbEvent.ContentBlock.Type == "tool_use" {
					toolCallIndex := len(toolCalls)
					toolCallOrder = append(toolCallOrder, toolCallIndex)
					contentBlockToToolCallIndex[cbEvent.Index] = toolCallIndex
					toolCalls[toolCallIndex] = openai.ToolCall{
						ID:   cbEvent.ContentBlock.ID,
						Type: "function",
						Function: openai.FunctionCall{
							Name:      cbEvent.ContentBlock.Name,
							Arguments: "",
						},
					}
					if err := writeToolCallStartChunk(w, rc, &captured, messageID, model, created, toolCallIndex, cbEvent.ContentBlock.ID, cbEvent.ContentBlock.Name); err != nil {
						return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
					}
				}
			}

		case "content_block_delta":
			var event anthropic.ContentBlockDeltaEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				slog.Warn("failed to parse content_block_delta event", "error", err)
				continue
			}

			// Handle thinking block deltas — emit as reasoning field.
			if thinkingBlockIndices[event.Index] {
				if event.Delta.Type == "thinking_delta" {
					delta := &openai.ChatMessage{
						Role:      "assistant",
						Content:   "",
						Reasoning: event.Delta.Thinking,
					}
					if err := writeChunk(w, rc, &captured, messageID, model, created, delta, nil, nil, nil); err != nil {
						return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
					}
				}
				continue
			}
			slog.Debug("content_block_delta", "index", event.Index, "delta_type", event.Delta.Type, "text_len", len(event.Delta.Text))

			switch event.Delta.Type {
			case "text_delta":
				delta := &openai.ChatMessage{
					Content: event.Delta.Text,
				}
				if err := writeChunk(w, rc, &captured, messageID, model, created, delta, nil, nil, nil); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
				}

			case "input_json_delta":
				toolCallIndex, ok := contentBlockToToolCallIndex[event.Index]
				if !ok {
					slog.Warn("input_json_delta for unmapped content block index", "index", event.Index)
					continue
				}
				if err := writeToolCallDeltaChunk(w, rc, &captured, messageID, model, created, toolCallIndex, event.Delta.PartialJSON); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
				}
			}

		case "content_block_stop":
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
			delta := &openai.ChatMessage{}
			if err := writeChunk(w, rc, &captured, messageID, model, created, delta, finishReason, nil, nil); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
			}

		case "message_stop":
			sentStop = true
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

// ---------------------------------------------------------------------------
// OpenAI → Anthropic SSE (ReverseSSEStream)
// ---------------------------------------------------------------------------

// ReverseSSEStream reads OpenAI SSE events from upstream, translates each to
// Anthropic-compatible SSE events, and writes them to the client.
func ReverseSSEStream(ctx context.Context, w http.ResponseWriter, upstream io.ReadCloser, model string) (*StreamResult, error) {
	defer func() { _ = upstream.Close() }()
	stop := sse.CloseOnCancel(ctx, upstream)
	defer stop()

	rc := http.NewResponseController(w)
	sse.WriteHeaders(w)

	var captured bytes.Buffer
	var inputTokens, outputTokens, cacheReadTokens, reasoningTokens int64
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
	textBlockIndex := -1     // -1 = no text block started yet
	thinkingBlockIndex := -1 // -1 = no thinking block started yet

	scanner := sse.NewScanner(upstream)
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]

		if data == "[DONE]" {
			// Close thinking block if open.
			if thinkingBlockIndex >= 0 {
				if err := sse.WriteEvent(w, rc, &captured, "content_block_stop",
					map[string]interface{}{"type": "content_block_stop", "index": thinkingBlockIndex}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
			}

			// Close text block if open.
			if textBlockIndex >= 0 {
				if err := sse.WriteEvent(w, rc, &captured, "content_block_stop",
					map[string]interface{}{"type": "content_block_stop", "index": textBlockIndex}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
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
				if err := sse.WriteEvent(w, rc, &captured, "content_block_stop",
					map[string]interface{}{"type": "content_block_stop", "index": e.state.blockIndex}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
			}

			// Map OpenAI finish_reason → Anthropic stop_reason.
			stopReason := "end_turn"
			if finishReason == "tool_calls" {
				stopReason = "tool_use"
			}

			if err := sse.WriteEvent(w, rc, &captured, "message_delta",
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
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}

			if err := sse.WriteEvent(w, rc, &captured, "message_stop",
				map[string]interface{}{"type": "message_stop"}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}

			sentDone = true
			continue
		}

		// Parse the OpenAI chunk.
		var chunk openai.ChatCompletionResponse
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
			if chunk.Usage.CompletionTokensDetails != nil {
				reasoningTokens = chunk.Usage.CompletionTokensDetails.ReasoningTokens
			}
			if reasoningTokens == 0 && chunk.Usage.ReasoningTokens > 0 {
				reasoningTokens = chunk.Usage.ReasoningTokens
			}
		}

		// Send message_start on first chunk.
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
						"usage": map[string]interface{}{
							"input_tokens":                0,
							"output_tokens":               0,
							"cache_creation_input_tokens": 0,
							"cache_read_input_tokens":     0,
						},
					},
				}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
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

		// Handle reasoning/thinking content deltas.
		thinkingText := delta.Reasoning
		if thinkingText == "" {
			thinkingText = delta.ReasoningContent
		}
		if thinkingText != "" {
			if thinkingBlockIndex < 0 {
				thinkingBlockIndex = nextBlockIndex
				nextBlockIndex++
				if err := sse.WriteEvent(w, rc, &captured, "content_block_start",
					map[string]interface{}{
						"type":          "content_block_start",
						"index":         thinkingBlockIndex,
						"content_block": map[string]interface{}{"type": "thinking", "thinking": ""},
					}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
			}

			if err := sse.WriteEvent(w, rc, &captured, "content_block_delta",
				map[string]interface{}{
					"type":  "content_block_delta",
					"index": thinkingBlockIndex,
					"delta": map[string]interface{}{"type": "thinking_delta", "thinking": thinkingText},
				}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}
		}

		// Handle text content deltas (string or []openai.ContentPart).
		var textContent string
		switch c := delta.Content.(type) {
		case string:
			textContent = c
		case []interface{}:
			for _, part := range c {
				if m, ok := part.(map[string]interface{}); ok {
					if t, ok := m["text"].(string); ok {
						textContent += t
					}
				}
			}
		}

		if textContent != "" {
			if textBlockIndex < 0 {
				textBlockIndex = nextBlockIndex
				nextBlockIndex++
				if err := sse.WriteEvent(w, rc, &captured, "content_block_start",
					map[string]interface{}{
						"type":          "content_block_start",
						"index":         textBlockIndex,
						"content_block": map[string]interface{}{"type": "text", "text": ""},
					}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
			}

			if err := sse.WriteEvent(w, rc, &captured, "content_block_delta",
				map[string]interface{}{
					"type":  "content_block_delta",
					"index": textBlockIndex,
					"delta": map[string]interface{}{"type": "text_delta", "text": textContent},
				}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
			}
		}

		// Handle refusal content — emit as text block.
		if delta.Refusal != "" {
			if textBlockIndex < 0 {
				textBlockIndex = nextBlockIndex
				nextBlockIndex++
				if err := sse.WriteEvent(w, rc, &captured, "content_block_start",
					map[string]interface{}{
						"type":          "content_block_start",
						"index":         textBlockIndex,
						"content_block": map[string]interface{}{"type": "text", "text": ""},
					}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
			}

			if err := sse.WriteEvent(w, rc, &captured, "content_block_delta",
				map[string]interface{}{
					"type":  "content_block_delta",
					"index": textBlockIndex,
					"delta": map[string]interface{}{"type": "text_delta", "text": delta.Refusal},
				}); err != nil {
				return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
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
				if err := sse.WriteEvent(w, rc, &captured, "content_block_start",
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
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
			}

			if tc.Function.Arguments != "" {
				if err := sse.WriteEvent(w, rc, &captured, "content_block_delta",
					map[string]interface{}{
						"type":  "content_block_delta",
						"index": state.blockIndex,
						"delta": map[string]interface{}{
							"type":         "input_json_delta",
							"partial_json": tc.Function.Arguments,
						},
					}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
			}
		}
	}

	if sentMessageStart && !sentDone {
		slog.Warn("upstream OpenAI stream ended without [DONE] after message_start was sent")
	}

	return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), scanner.Err()
}

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

	scanner := sse.NewScanner(upstream)
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]

		if data == "[DONE]" {
			finalText := fullText.String()

			if sentContentPartAdded {
				if err := sse.WriteEvent(w, rc, &captured, "response.output_text.done", map[string]interface{}{
					"type":          "response.output_text.done",
					"item_id":       messageID,
					"output_index":  0,
					"content_index": 0,
					"text":          finalText,
				}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}

				if err := sse.WriteEvent(w, rc, &captured, "response.content_part.done", map[string]interface{}{
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

			if sentOutputItemAdded {
				if err := sse.WriteEvent(w, rc, &captured, "response.output_item.done", map[string]interface{}{
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

		if !sentOutputItemAdded {
			if err := sse.WriteEvent(w, rc, &captured, "response.output_item.added", map[string]interface{}{
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

		if text, ok := delta.Content.(string); ok && text != "" {
			if !sentContentPartAdded {
				if err := sse.WriteEvent(w, rc, &captured, "response.content_part.added", map[string]interface{}{
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
			if err := sse.WriteEvent(w, rc, &captured, "response.output_text.delta", map[string]interface{}{
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

		var gr gemini.GeminiResponse
		if err := json.Unmarshal([]byte(data), &gr); err != nil {
			slog.Warn("failed to parse Gemini SSE event", "error", err, "data", data[:min(len(data), 256)])
			continue
		}

		if gr.UsageMetadata != nil {
			inputTokens, outputTokens, cacheReadTokens, reasoningTokens = gemini.GeminiUsageTotals(gr.UsageMetadata)
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
					toolID := gemini.EncodeGeminiToolCallID(fmt.Sprintf("toolu_%06d", toolUseIdx), part.ThoughtSignature)
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

		var gr gemini.GeminiResponse
		if err := json.Unmarshal([]byte(data), &gr); err != nil {
			slog.Warn("failed to parse Gemini SSE event (OpenAI path)", "error", err, "data", data[:min(len(data), 256)])
			continue
		}

		if gr.UsageMetadata != nil {
			inputTokens, outputTokens, cacheReadTokens, reasoningTokens = gemini.GeminiUsageTotals(gr.UsageMetadata)
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
				callID := gemini.EncodeGeminiToolCallID(fmt.Sprintf("call_%06d", tcIdx), part.ThoughtSignature)
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

		var gr gemini.GeminiResponse
		if err := json.Unmarshal([]byte(data), &gr); err != nil {
			slog.Warn("failed to parse Gemini SSE event (Responses path)", "error", err, "data", data[:min(len(data), 256)])
			continue
		}

		if gr.UsageMetadata != nil {
			inputTokens, outputTokens, cacheReadTokens, reasoningTokens = gemini.GeminiUsageTotals(gr.UsageMetadata)
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
				callID := gemini.EncodeGeminiToolCallID(fmt.Sprintf("call_%06d", toolUseIdx), part.ThoughtSignature)
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
