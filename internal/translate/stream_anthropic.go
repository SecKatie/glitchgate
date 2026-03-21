// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

// stream_anthropic.go contains Anthropic↔OpenAI SSE stream translation:
//   - Anthropic → OpenAI (SSEStream)
//   - OpenAI → Anthropic (ReverseSSEStream)

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
	"github.com/seckatie/glitchgate/internal/provider/openai"
	"github.com/seckatie/glitchgate/internal/sse"
)

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
	textBlockSeen := false // once text starts, suppress late-arriving thinking deltas

	// Track active tool calls by index for streaming tool call support.
	toolCalls := make(map[int]openai.ToolCall)
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
			slog.Warn("failed to parse Anthropic SSE event type", "error", err, "data", data[:min(len(data), 256)]) //nolint:gosec // structured slog prevents log injection
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
				switch cbEvent.ContentBlock.Type {
				case "thinking":
					thinkingBlockIndices[cbEvent.Index] = true
					// Suppress late-arriving thinking blocks after text has
					// started (e.g. Kimi-K2.5 interleaves thinking/text blocks).
					if textBlockSeen {
						continue
					}
					// Suppress: the initial role chunk was already sent;
					// reasoning content will arrive via thinking_delta events.
				case "text":
					textBlockSeen = true
				case "tool_use":
					toolCallIndex := len(toolCalls)
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
			// Suppress if text content has already started (interleave guard).
			if thinkingBlockIndices[event.Index] {
				if event.Delta.Type == "thinking_delta" && !textBlockSeen {
					if err := writeChunk(w, rc, &captured, messageID, model, created, &openai.ChatMessage{
						Reasoning: event.Delta.Thinking,
					}, nil, nil, nil); err != nil {
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
	thinkingBlockClosed := false
	var reasoningTextLen int // accumulated reasoning text length for token estimation

	scanner := sse.NewScanner(upstream)
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]

		if data == "[DONE]" {
			// Close thinking block if open and not already closed.
			if thinkingBlockIndex >= 0 && !thinkingBlockClosed {
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
			slog.Warn("failed to parse upstream SSE chunk", "error", err, "data", data[:min(len(data), 256)]) //nolint:gosec // structured slog prevents log injection
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
		// Drop straggler reasoning that arrives after the thinking block
		// was closed (e.g. Kimi-K2.5 interleaves reasoning and content).
		thinkingText := delta.Reasoning
		if thinkingText == "" {
			thinkingText = delta.ReasoningContent
		}
		if thinkingText != "" && !thinkingBlockClosed {
			reasoningTextLen += len(thinkingText)
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
			// Close the thinking block before starting text content.
			// Some upstreams (e.g. Kimi-K2.5) interleave reasoning and
			// content, but Anthropic requires thinking to finish first.
			if thinkingBlockIndex >= 0 && !thinkingBlockClosed {
				if err := sse.WriteEvent(w, rc, &captured, "content_block_stop",
					map[string]interface{}{"type": "content_block_stop", "index": thinkingBlockIndex}); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), err
				}
				thinkingBlockClosed = true
			}

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

	// Estimate reasoning tokens from accumulated text when upstream
	// doesn't report them (e.g. Kimi-K2.5 sends no usage object).
	if reasoningTokens == 0 && reasoningTextLen > 0 {
		reasoningTokens = int64(reasoningTextLen+3) / 4
	}

	return buildResult(&captured, inputTokens, outputTokens, 0, cacheReadTokens, reasoningTokens), scanner.Err()
}
