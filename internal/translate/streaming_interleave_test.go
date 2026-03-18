// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// buildOpenAISSEChunk returns a single OpenAI SSE data line.
func buildOpenAISSEChunk(id, model string, role, content, reasoning string, finishReason *string) string {
	delta := map[string]interface{}{}
	if role != "" {
		delta["role"] = role
	}
	if content != "" || reasoning == "" {
		delta["content"] = content
	}
	if reasoning != "" {
		delta["reasoning"] = reasoning
	}

	choice := map[string]interface{}{
		"index": 0,
		"delta": delta,
	}
	if finishReason != nil {
		choice["finish_reason"] = *finishReason
	} else {
		choice["finish_reason"] = nil
	}

	chunk := map[string]interface{}{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": 1773848966,
		"model":   model,
		"choices": []interface{}{choice},
	}
	data, _ := json.Marshal(chunk)
	return fmt.Sprintf("data: %s", data)
}

func sPtr(s string) *string { return &s }

// buildInterleavedSSE creates an SSE stream that mimics Kimi-K2.5's behavior:
// reasoning chunks, then interleaved reasoning+content, then pure content.
func buildInterleavedSSE() string {
	id := "chatcmpl-test123"
	model := "s/Kimi-K2.5"

	lines := []string{
		buildOpenAISSEChunk(id, model, "assistant", "", "", nil),
		buildOpenAISSEChunk(id, model, "", "", "I need to think", nil),
		buildOpenAISSEChunk(id, model, "", "", " about this", nil),
		buildOpenAISSEChunk(id, model, "", "", " carefully.", nil),
		// Content starts — reasoning should close
		buildOpenAISSEChunk(id, model, "", "Hello", "", nil),
		// Straggler reasoning after content started — should be dropped
		buildOpenAISSEChunk(id, model, "", "", " straggler", nil),
		buildOpenAISSEChunk(id, model, "", " world!", "", nil),
		buildOpenAISSEChunk(id, model, "", "", "", sPtr("stop")),
		"data: [DONE]",
		"",
	}
	return strings.Join(lines, "\n")
}

// buildNormalReasoningSSE creates an SSE stream where reasoning completes
// before content starts (no interleaving).
func buildNormalReasoningSSE() string {
	id := "chatcmpl-normal123"
	model := "test-model"

	lines := []string{
		buildOpenAISSEChunk(id, model, "assistant", "", "", nil),
		buildOpenAISSEChunk(id, model, "", "", "Step 1: think", nil),
		buildOpenAISSEChunk(id, model, "", "", " Step 2: plan", nil),
		// Content starts after reasoning — no interleaving
		buildOpenAISSEChunk(id, model, "", "The answer is 42.", "", nil),
		buildOpenAISSEChunk(id, model, "", "", "", sPtr("stop")),
		"data: [DONE]",
		"",
	}
	return strings.Join(lines, "\n")
}

// buildNoReasoningSSE creates a plain content-only SSE stream.
func buildNoReasoningSSE() string {
	id := "chatcmpl-plain456"
	model := "test-model"

	lines := []string{
		buildOpenAISSEChunk(id, model, "assistant", "", "", nil),
		buildOpenAISSEChunk(id, model, "", "Just content.", "", nil),
		buildOpenAISSEChunk(id, model, "", "", "", sPtr("stop")),
		"data: [DONE]",
		"",
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// ReverseSSEStream (OpenAI → Anthropic)
// ---------------------------------------------------------------------------

func TestReverseSSEStream_InterleavedReasoning(t *testing.T) {
	rec := httptest.NewRecorder()
	upstream := io.NopCloser(strings.NewReader(buildInterleavedSSE()))

	result, err := ReverseSSEStream(context.Background(), rec, upstream, "s/Kimi-K2.5")
	require.NoError(t, err)

	body := rec.Body.String()

	// Thinking block should exist.
	require.Contains(t, body, `"type":"thinking"`, "should have thinking block")

	// Content should appear.
	require.Contains(t, body, `"type":"text_delta"`, "should have text deltas")

	// Parse events to verify ordering.
	events := parseSSEEvents(t, body)

	// Find the thinking block stop — it must come BEFORE any text_delta.
	thinkingStopIdx := -1
	firstTextDeltaIdx := -1
	for i, ev := range events {
		if ev.eventType == "content_block_stop" {
			var payload map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(ev.data), &payload))
			if idx, ok := payload["index"].(float64); ok && idx == 0 {
				thinkingStopIdx = i
			}
		}
		if ev.eventType == "content_block_delta" && firstTextDeltaIdx == -1 {
			var payload map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(ev.data), &payload))
			if delta, ok := payload["delta"].(map[string]interface{}); ok {
				if delta["type"] == "text_delta" {
					firstTextDeltaIdx = i
				}
			}
		}
	}

	require.Greater(t, thinkingStopIdx, -1, "thinking block stop should be emitted")
	require.Greater(t, firstTextDeltaIdx, -1, "text delta should be emitted")
	require.Less(t, thinkingStopIdx, firstTextDeltaIdx,
		"thinking block must be closed before text deltas start")

	// Straggler reasoning should be dropped — no thinking_delta after text starts.
	for i := firstTextDeltaIdx; i < len(events); i++ {
		if events[i].eventType == "content_block_delta" {
			var payload map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(events[i].data), &payload))
			if delta, ok := payload["delta"].(map[string]interface{}); ok {
				require.NotEqual(t, "thinking_delta", delta["type"],
					"no thinking_delta should appear after text started")
			}
		}
	}

	// Reasoning token estimation should produce a non-zero value.
	require.Greater(t, result.ReasoningTokens, int64(0),
		"reasoning tokens should be estimated from text length")
}

func TestReverseSSEStream_NormalReasoning(t *testing.T) {
	rec := httptest.NewRecorder()
	upstream := io.NopCloser(strings.NewReader(buildNormalReasoningSSE()))

	result, err := ReverseSSEStream(context.Background(), rec, upstream, "test-model")
	require.NoError(t, err)

	body := rec.Body.String()

	require.Contains(t, body, `"type":"thinking"`)
	require.Contains(t, body, `"type":"text_delta"`)

	events := parseSSEEvents(t, body)

	// Verify thinking block is closed before text starts.
	thinkingStopIdx := -1
	firstTextDeltaIdx := -1
	for i, ev := range events {
		if ev.eventType == "content_block_stop" {
			var payload map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(ev.data), &payload))
			if idx, ok := payload["index"].(float64); ok && idx == 0 {
				thinkingStopIdx = i
			}
		}
		if ev.eventType == "content_block_delta" && firstTextDeltaIdx == -1 {
			var payload map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(ev.data), &payload))
			if delta, ok := payload["delta"].(map[string]interface{}); ok {
				if delta["type"] == "text_delta" {
					firstTextDeltaIdx = i
				}
			}
		}
	}

	require.Greater(t, thinkingStopIdx, -1)
	require.Greater(t, firstTextDeltaIdx, -1)
	require.Less(t, thinkingStopIdx, firstTextDeltaIdx)

	require.Greater(t, result.ReasoningTokens, int64(0))
}

func TestReverseSSEStream_NoReasoning(t *testing.T) {
	rec := httptest.NewRecorder()
	upstream := io.NopCloser(strings.NewReader(buildNoReasoningSSE()))

	result, err := ReverseSSEStream(context.Background(), rec, upstream, "test-model")
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"type":"text_delta"`)
	require.NotContains(t, body, `"type":"thinking"`,
		"no thinking block when upstream sends no reasoning")

	require.Equal(t, int64(0), result.ReasoningTokens)
}

// ---------------------------------------------------------------------------
// OpenAISSEToResponsesSSE (OpenAI → Responses API)
// ---------------------------------------------------------------------------

func TestOpenAISSEToResponsesSSE_WithReasoning(t *testing.T) {
	rec := httptest.NewRecorder()
	upstream := io.NopCloser(strings.NewReader(buildNormalReasoningSSE()))

	result, err := OpenAISSEToResponsesSSE(rec, upstream, "test-model")
	require.NoError(t, err)

	body := rec.Body.String()

	// Should have reasoning output item.
	require.Contains(t, body, `"type":"reasoning"`, "should emit reasoning output item")
	require.Contains(t, body, "response.reasoning_summary_part.done",
		"should emit reasoning summary done event")
	require.Contains(t, body, "response.output_text.delta",
		"should have text content")

	// Parse response.completed to verify output array includes reasoning.
	completedEvent := extractResponseCompletedEvent(t, body)
	response, ok := completedEvent["response"].(map[string]interface{})
	require.True(t, ok)

	output, ok := response["output"].([]interface{})
	require.True(t, ok)
	require.Len(t, output, 2, "output should have reasoning + message items")

	firstItem, ok := output[0].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "reasoning", firstItem["type"])

	secondItem, ok := output[1].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "message", secondItem["type"])

	require.Greater(t, result.ReasoningTokens, int64(0))
}

func TestOpenAISSEToResponsesSSE_InterleavedReasoning(t *testing.T) {
	rec := httptest.NewRecorder()
	upstream := io.NopCloser(strings.NewReader(buildInterleavedSSE()))

	result, err := OpenAISSEToResponsesSSE(rec, upstream, "s/Kimi-K2.5")
	require.NoError(t, err)

	body := rec.Body.String()

	// Reasoning should be present and completed before text.
	require.Contains(t, body, `"type":"reasoning"`)

	// Parse events — reasoning item.done must come before text delta.
	events := parseSSEEvents(t, body)

	reasoningDoneIdx := -1
	firstTextDeltaIdx := -1
	for i, ev := range events {
		if ev.eventType == "response.output_item.done" && reasoningDoneIdx == -1 {
			var payload map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(ev.data), &payload))
			if item, ok := payload["item"].(map[string]interface{}); ok {
				if item["type"] == "reasoning" {
					reasoningDoneIdx = i
				}
			}
		}
		if ev.eventType == "response.output_text.delta" && firstTextDeltaIdx == -1 {
			firstTextDeltaIdx = i
		}
	}

	require.Greater(t, reasoningDoneIdx, -1, "reasoning output item should be completed")
	require.Greater(t, firstTextDeltaIdx, -1, "text delta should be emitted")
	require.Less(t, reasoningDoneIdx, firstTextDeltaIdx,
		"reasoning must be completed before text deltas start")

	require.Greater(t, result.ReasoningTokens, int64(0))
}

func TestOpenAISSEToResponsesSSE_NoReasoning(t *testing.T) {
	rec := httptest.NewRecorder()
	upstream := io.NopCloser(strings.NewReader(buildNoReasoningSSE()))

	result, err := OpenAISSEToResponsesSSE(rec, upstream, "test-model")
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, "response.output_text.delta")
	require.NotContains(t, body, `"type":"reasoning"`,
		"no reasoning when upstream sends none")

	// response.completed should have single message output item.
	completedEvent := extractResponseCompletedEvent(t, body)
	response, ok := completedEvent["response"].(map[string]interface{})
	require.True(t, ok)

	output, ok := response["output"].([]interface{})
	require.True(t, ok)
	require.Len(t, output, 1, "output should have only message item")

	require.Equal(t, int64(0), result.ReasoningTokens)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type sseEvent struct {
	eventType string
	data      string
}

// parseSSEEvents splits SSE output into typed events.
func parseSSEEvents(t *testing.T, body string) []sseEvent {
	t.Helper()
	lines := strings.Split(body, "\n")
	var events []sseEvent
	var currentType string
	for _, line := range lines {
		if strings.HasPrefix(line, "event: ") {
			currentType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			events = append(events, sseEvent{
				eventType: currentType,
				data:      line[6:],
			})
			currentType = ""
		}
	}
	return events
}

// buildAnthropicSSEEvent builds a single Anthropic SSE event line.
func buildAnthropicSSEEvent(eventType string, payload interface{}) string {
	data, _ := json.Marshal(payload)
	return fmt.Sprintf("event: %s\ndata: %s\n", eventType, data)
}

// buildAnthropicInterleavedSSE creates an Anthropic SSE stream that mimics the
// upstream pattern observed from Kimi-K2.5 via Synthetic:
// thinking(0) → text(1) → thinking(2) → text(3)
func buildAnthropicInterleavedSSE() string {
	lines := []string{
		buildAnthropicSSEEvent("message_start", map[string]interface{}{
			"type": "message_start",
			"message": map[string]interface{}{
				"id":      "msg_test123",
				"type":    "message",
				"role":    "assistant",
				"content": []interface{}{},
				"model":   "test-model",
				"usage":   map[string]interface{}{"input_tokens": 100, "output_tokens": 0},
			},
		}),
		// Block 0: thinking
		buildAnthropicSSEEvent("content_block_start", map[string]interface{}{
			"type":          "content_block_start",
			"index":         0,
			"content_block": map[string]interface{}{"type": "thinking", "thinking": ""},
		}),
		buildAnthropicSSEEvent("content_block_delta", map[string]interface{}{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]interface{}{"type": "thinking_delta", "thinking": "I need to think"},
		}),
		buildAnthropicSSEEvent("content_block_stop", map[string]interface{}{
			"type": "content_block_stop", "index": 0,
		}),
		// Block 1: text (first word)
		buildAnthropicSSEEvent("content_block_start", map[string]interface{}{
			"type":          "content_block_start",
			"index":         1,
			"content_block": map[string]interface{}{"type": "text", "text": ""},
		}),
		buildAnthropicSSEEvent("content_block_delta", map[string]interface{}{
			"type":  "content_block_delta",
			"index": 1,
			"delta": map[string]interface{}{"type": "text_delta", "text": "Hello"},
		}),
		buildAnthropicSSEEvent("content_block_stop", map[string]interface{}{
			"type": "content_block_stop", "index": 1,
		}),
		// Block 2: late thinking (should be suppressed)
		buildAnthropicSSEEvent("content_block_start", map[string]interface{}{
			"type":          "content_block_start",
			"index":         2,
			"content_block": map[string]interface{}{"type": "thinking", "thinking": ""},
		}),
		buildAnthropicSSEEvent("content_block_delta", map[string]interface{}{
			"type":  "content_block_delta",
			"index": 2,
			"delta": map[string]interface{}{"type": "thinking_delta", "thinking": " straggler"},
		}),
		buildAnthropicSSEEvent("content_block_stop", map[string]interface{}{
			"type": "content_block_stop", "index": 2,
		}),
		// Block 3: text (rest of response)
		buildAnthropicSSEEvent("content_block_start", map[string]interface{}{
			"type":          "content_block_start",
			"index":         3,
			"content_block": map[string]interface{}{"type": "text", "text": ""},
		}),
		buildAnthropicSSEEvent("content_block_delta", map[string]interface{}{
			"type":  "content_block_delta",
			"index": 3,
			"delta": map[string]interface{}{"type": "text_delta", "text": " world!"},
		}),
		buildAnthropicSSEEvent("content_block_stop", map[string]interface{}{
			"type": "content_block_stop", "index": 3,
		}),
		buildAnthropicSSEEvent("message_delta", map[string]interface{}{
			"type":  "message_delta",
			"delta": map[string]interface{}{"stop_reason": "end_turn"},
			"usage": map[string]interface{}{"output_tokens": 50},
		}),
		buildAnthropicSSEEvent("message_stop", map[string]interface{}{"type": "message_stop"}),
		"",
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// SSEStream (Anthropic → OpenAI) interleave tests
// ---------------------------------------------------------------------------

func TestSSEStream_InterleavedThinkingBlocks(t *testing.T) {
	rec := httptest.NewRecorder()
	upstream := io.NopCloser(strings.NewReader(buildAnthropicInterleavedSSE()))

	_, err := SSEStream(rec, upstream, "test-model")
	require.NoError(t, err)

	body := rec.Body.String()

	// Parse all OpenAI chunks from the output.
	var chunks []map[string]interface{}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: ") && line[6:] != "[DONE]" {
			var obj map[string]interface{}
			if json.Unmarshal([]byte(line[6:]), &obj) == nil {
				chunks = append(chunks, obj)
			}
		}
	}

	// Collect all reasoning and content deltas in order.
	var reasoning, content []string
	var reasoningAfterContent []string
	contentStarted := false
	for _, chunk := range chunks {
		choices, ok := chunk["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			continue
		}
		choice, ok := choices[0].(map[string]interface{})
		if !ok {
			continue
		}
		delta, ok := choice["delta"].(map[string]interface{})
		if !ok {
			continue
		}
		if r, ok := delta["reasoning"].(string); ok && r != "" {
			reasoning = append(reasoning, r)
			if contentStarted {
				reasoningAfterContent = append(reasoningAfterContent, r)
			}
		}
		if c, ok := delta["content"].(string); ok && c != "" {
			content = append(content, c)
			contentStarted = true
		}
	}

	// First thinking block should be present.
	require.NotEmpty(t, reasoning, "should have reasoning deltas from first thinking block")
	require.Contains(t, strings.Join(reasoning, ""), "I need to think")

	// Both text blocks should be present.
	require.NotEmpty(t, content, "should have content deltas")
	fullContent := strings.Join(content, "")
	require.Contains(t, fullContent, "Hello")
	require.Contains(t, fullContent, "world!")

	// Late-arriving thinking (" straggler") must NOT appear after content.
	require.Empty(t, reasoningAfterContent,
		"no reasoning deltas should appear after content started; got: %v", reasoningAfterContent)
}
