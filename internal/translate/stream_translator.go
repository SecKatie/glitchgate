package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
	"github.com/seckatie/glitchgate/internal/sse"
)

// StreamResult is a type alias for sse.StreamResult, preserving backward
// compatibility for callers that reference translate.StreamResult.
type StreamResult = sse.StreamResult

// writeChunk is a helper that builds a chunk, writes it to the client,
// and returns an error (with partial result) if writing fails.
// This consolidates the repeated pattern of buildChunk → writeSSEChunk → return buildResult on error.
func writeChunk(w http.ResponseWriter, rc *http.ResponseController, captured *bytes.Buffer, id, model string, created int64, delta *ChatMessage, finishReason *string, toolCalls map[int]ToolCall, toolCallOrder []int) error {
	var chunk *ChatCompletionResponse
	if len(toolCalls) > 0 {
		chunk = buildChunkWithToolCalls(id, model, created, delta, finishReason, toolCalls, toolCallOrder)
	} else {
		chunk = buildChunk(id, model, created, delta, finishReason)
	}
	return writeSSEChunk(w, rc, captured, chunk)
}

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
	toolCalls := make(map[int]ToolCall)
	toolCallOrder := []int{} // track order of tool calls

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
				delta := &ChatMessage{
					Role:    "assistant",
					Content: "",
				}
				if err := writeChunk(w, rc, &captured, messageID, model, created, delta, nil, nil, nil); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
				}
				sentInitial = true
			}

		case "content_block_start":
			// Track thinking blocks by index so we can emit reasoning.
			// Also track tool_use blocks for streaming tool call support.
			var cbEvent anthropic.ContentBlockStartEvent
			if err := json.Unmarshal([]byte(data), &cbEvent); err != nil {
				slog.Warn("failed to parse content_block_start event", "error", err)
			} else {
				slog.Debug("content_block_start", "index", cbEvent.Index, "type", cbEvent.ContentBlock.Type)
				if cbEvent.ContentBlock.Type == "thinking" {
					thinkingBlockIndices[cbEvent.Index] = true
					// Emit initial chunk with reasoning field.
					delta := &ChatMessage{
						Role:      "assistant",
						Content:   "",
						Reasoning: "",
					}
					if err := writeChunk(w, rc, &captured, messageID, model, created, delta, nil, nil, nil); err != nil {
						return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
					}
				} else if cbEvent.ContentBlock.Type == "tool_use" {
					// Track order of tool calls.
					toolCallOrder = append(toolCallOrder, cbEvent.Index)
					// Initialize a new tool call.
					toolCalls[cbEvent.Index] = ToolCall{
						ID:   cbEvent.ContentBlock.ID,
						Type: "function",
						Function: FunctionCall{
							Name:      cbEvent.ContentBlock.Name,
							Arguments: "",
						},
					}
					// Emit initial chunk with tool call ID and name.
					delta := &ChatMessage{
						Role:    "assistant",
						Content: "",
					}
					if err := writeChunk(w, rc, &captured, messageID, model, created, delta, nil, toolCalls, toolCallOrder); err != nil {
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
					delta := &ChatMessage{
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
				delta := &ChatMessage{
					Content: event.Delta.Text,
				}
				if err := writeChunk(w, rc, &captured, messageID, model, created, delta, nil, nil, nil); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
				}

			case "input_json_delta":
				// Emit chunk with ONLY the new arguments delta, not accumulated string.
				if err := writeToolCallDeltaChunk(w, rc, &captured, messageID, model, created, event.Index, event.Delta.PartialJSON); err != nil {
					return buildResult(&captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, 0), err
				}
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
			delta := &ChatMessage{}
			if err := writeChunk(w, rc, &captured, messageID, model, created, delta, finishReason, nil, nil); err != nil {
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

// buildChunkWithToolCalls creates a ChatCompletionResponse with tool calls.
func buildChunkWithToolCalls(id, model string, created int64, delta *ChatMessage, finishReason *string, toolCalls map[int]ToolCall, order []int) *ChatCompletionResponse {
	// Build tool calls in order.
	tcSlice := make([]ToolCall, 0, len(toolCalls))
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

// writeToolCallDeltaChunk emits a chunk with only the incremental tool call arguments delta,
// not the accumulated full string. This follows OpenAI's streaming spec where tool call deltas
// only include the new arguments, not the full accumulated arguments.
func writeToolCallDeltaChunk(w http.ResponseWriter, rc *http.ResponseController, captured *bytes.Buffer, id, model string, created int64, toolCallIndex int, newArgs string) error {
	delta := &ChatMessage{
		Role:    "assistant",
		Content: "",
		ToolCalls: []ToolCall{
			{
				ID:   "",
				Type: "function",
				Function: FunctionCall{
					Name:      "",
					Arguments: newArgs, // Only the delta, not accumulated
				},
				Index: &toolCallIndex,
			},
		},
	}

	chunk := &ChatCompletionResponse{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []Choice{
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
func buildResult(captured *bytes.Buffer, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, reasoningTokens int64) *sse.StreamResult {
	return sse.BuildResult(captured, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, reasoningTokens)
}
