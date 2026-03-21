// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

// streaming.go contains shared helpers for SSE stream translation.
// Format-specific translation functions are in:
//   - stream_anthropic.go (Anthropic ↔ OpenAI)
//   - stream_responses.go (Responses API translations)
//   - stream_gemini.go    (Gemini translations)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

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
