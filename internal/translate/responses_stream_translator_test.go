// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnthropicSSEToResponsesSSE_CachedTokens(t *testing.T) {
	// Feed a minimal Anthropic SSE stream with cache_read_input_tokens in message_start.
	sseInput := strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"msg_01","usage":{"input_tokens":20,"cache_read_input_tokens":80,"output_tokens":0}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_delta","usage":{"output_tokens":5}}`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	rec := httptest.NewRecorder()
	upstream := io.NopCloser(strings.NewReader(sseInput))

	result, err := AnthropicSSEToResponsesSSE(rec, upstream, "claude-3-5-sonnet")
	require.NoError(t, err)

	// StreamResult should have cacheReadTokens populated.
	require.Equal(t, int64(80), result.CacheReadInputTokens)

	// Parse the response.completed event from the captured SSE output.
	body := rec.Body.String()
	completedEvent := extractResponseCompletedEvent(t, body)

	response, ok := completedEvent["response"].(map[string]interface{})
	require.True(t, ok)

	usage, ok := response["usage"].(map[string]interface{})
	require.True(t, ok)

	details, ok := usage["input_tokens_details"].(map[string]interface{})
	require.True(t, ok, "input_tokens_details should be present when caching occurred")
	require.Equal(t, float64(80), details["cached_tokens"])
}

func TestAnthropicSSEToResponsesSSE_NoCachedTokens(t *testing.T) {
	sseInput := strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"msg_02","usage":{"input_tokens":20,"output_tokens":0}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"message_delta","usage":{"output_tokens":5}}`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	rec := httptest.NewRecorder()
	upstream := io.NopCloser(strings.NewReader(sseInput))

	result, err := AnthropicSSEToResponsesSSE(rec, upstream, "claude-3-5-sonnet")
	require.NoError(t, err)
	require.Equal(t, int64(0), result.CacheReadInputTokens)

	body := rec.Body.String()
	completedEvent := extractResponseCompletedEvent(t, body)

	response, ok := completedEvent["response"].(map[string]interface{})
	require.True(t, ok)

	usage, ok := response["usage"].(map[string]interface{})
	require.True(t, ok)

	_, hasDetails := usage["input_tokens_details"]
	require.False(t, hasDetails, "input_tokens_details should be absent when no caching occurred")
}

func TestOpenAISSEToResponsesSSE_CachedTokens(t *testing.T) {
	// Feed an OpenAI SSE stream where the final usage chunk includes prompt_tokens_details.
	sseInput := strings.Join([]string{
		`data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1710288000,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1710288000,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110,"prompt_tokens_details":{"cached_tokens":75}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	rec := httptest.NewRecorder()
	upstream := io.NopCloser(strings.NewReader(sseInput))

	result, err := OpenAISSEToResponsesSSE(rec, upstream, "gpt-4o")
	require.NoError(t, err)

	require.Equal(t, int64(75), result.CacheReadInputTokens)

	body := rec.Body.String()
	completedEvent := extractResponseCompletedEvent(t, body)

	response, ok := completedEvent["response"].(map[string]interface{})
	require.True(t, ok)

	usage, ok := response["usage"].(map[string]interface{})
	require.True(t, ok)

	details, ok := usage["input_tokens_details"].(map[string]interface{})
	require.True(t, ok, "input_tokens_details should be present when caching occurred")
	require.Equal(t, float64(75), details["cached_tokens"])
}

func TestOpenAISSEToResponsesSSE_NoCachedTokens(t *testing.T) {
	sseInput := strings.Join([]string{
		`data: {"id":"chatcmpl-xyz","object":"chat.completion.chunk","created":1710288000,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-xyz","object":"chat.completion.chunk","created":1710288000,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	rec := httptest.NewRecorder()
	upstream := io.NopCloser(strings.NewReader(sseInput))

	result, err := OpenAISSEToResponsesSSE(rec, upstream, "gpt-4o")
	require.NoError(t, err)
	require.Equal(t, int64(0), result.CacheReadInputTokens)

	body := rec.Body.String()
	completedEvent := extractResponseCompletedEvent(t, body)

	response, ok := completedEvent["response"].(map[string]interface{})
	require.True(t, ok)

	usage, ok := response["usage"].(map[string]interface{})
	require.True(t, ok)

	_, hasDetails := usage["input_tokens_details"]
	require.False(t, hasDetails, "input_tokens_details should be absent when no caching occurred")
}

func TestResponsesSSEToAnthropicSSE_PreservesReasoningBlocks(t *testing.T) {
	sseInput := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_01","model":"gpt-4o"}}`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[],"status":"in_progress"}}`,
		`data: {"type":"response.reasoning_summary_part.done","output_index":0,"item_id":"rs_1","summary_index":0,"part":{"type":"summary_text","text":"I reasoned about it."}}`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"I reasoned about it."}],"status":"completed"}}`,
		`data: {"type":"response.content_part.added","item_id":"msg_1","output_index":1,"content_index":0,"part":{"type":"output_text","text":""}}`,
		`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":1,"content_index":0,"delta":"Final answer."}`,
		`data: {"type":"response.content_part.done","item_id":"msg_1","output_index":1,"content_index":0,"part":{"type":"output_text","text":"Final answer."}}`,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":20,"output_tokens":5,"input_tokens_details":{"cached_tokens":8}}}}`,
		``,
	}, "\n")

	rec := httptest.NewRecorder()
	upstream := io.NopCloser(strings.NewReader(sseInput))

	result, err := ResponsesSSEToAnthropicSSE(rec, upstream, "claude-sonnet")
	require.NoError(t, err)
	require.Equal(t, int64(8), result.CacheReadInputTokens)

	body := rec.Body.String()
	require.Contains(t, body, `"type":"thinking"`)
	require.Contains(t, body, `"thinking":"I reasoned about it."`)
	require.Contains(t, body, `"index":0,"type":"content_block_stop"`)
	require.Contains(t, body, `"type":"text"`)
	require.Contains(t, body, `"text":"Final answer."`)
	require.Contains(t, body, `"type":"message_stop"`)
}

// extractResponseCompletedEvent scans SSE output for the response.completed event
// and returns its parsed data payload.
func extractResponseCompletedEvent(t *testing.T, sseBody string) map[string]interface{} {
	t.Helper()
	lines := strings.Split(sseBody, "\n")
	for i, line := range lines {
		if line == "event: response.completed" && i+1 < len(lines) {
			dataLine := lines[i+1]
			if strings.HasPrefix(dataLine, "data: ") {
				var payload map[string]interface{}
				require.NoError(t, json.Unmarshal([]byte(dataLine[6:]), &payload))
				return payload
			}
		}
	}
	t.Fatal("response.completed event not found in SSE output")
	return nil
}
