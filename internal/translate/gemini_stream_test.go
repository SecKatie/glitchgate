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

func TestGeminiUsageTotals(t *testing.T) {
	t.Parallel()

	input, output, cacheRead, reasoning := GeminiUsageTotals(&GeminiUsageMetadata{
		PromptTokenCount:        42,
		CachedContentTokenCount: 10,
		CandidatesTokenCount:    17,
		ThoughtsTokenCount:      4,
	})

	require.Equal(t, int64(32), input)
	require.Equal(t, int64(21), output)
	require.Equal(t, int64(10), cacheRead)
	require.Equal(t, int64(4), reasoning)
}

func TestGeminiSSEToResponsesSSE_UsageIncludesCacheAndReasoning(t *testing.T) {
	t.Parallel()

	sseInput := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":42,"cachedContentTokenCount":10,"candidatesTokenCount":17,"thoughtsTokenCount":4,"totalTokenCount":59}}`,
		``,
	}, "\n")

	rec := httptest.NewRecorder()
	upstream := io.NopCloser(strings.NewReader(sseInput))

	result, err := GeminiSSEToResponsesSSE(rec, upstream, "gemini-2.5-pro")
	require.NoError(t, err)
	require.Equal(t, int64(32), result.InputTokens)
	require.Equal(t, int64(21), result.OutputTokens)
	require.Equal(t, int64(10), result.CacheReadInputTokens)
	require.Equal(t, int64(4), result.ReasoningTokens)

	completed := extractGeminiResponseCompletedEvent(t, rec.Body.String())
	response, ok := completed["response"].(map[string]interface{})
	require.True(t, ok)

	usage, ok := response["usage"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, float64(42), usage["input_tokens"])
	require.Equal(t, float64(21), usage["output_tokens"])
	require.Equal(t, float64(63), usage["total_tokens"])

	inputDetails, ok := usage["input_tokens_details"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, float64(10), inputDetails["cached_tokens"])

	outputDetails, ok := usage["output_tokens_details"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, float64(4), outputDetails["reasoning_tokens"])
}

func TestGeminiSSEToAnthropicSSE_StreamResultIncludesCacheAndReasoning(t *testing.T) {
	t.Parallel()

	sseInput := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":42,"cachedContentTokenCount":10,"candidatesTokenCount":17,"thoughtsTokenCount":4}}`,
		``,
	}, "\n")

	rec := httptest.NewRecorder()
	upstream := io.NopCloser(strings.NewReader(sseInput))

	result, err := GeminiSSEToAnthropicSSE(rec, upstream, "gemini-2.5-pro")
	require.NoError(t, err)
	require.Equal(t, int64(32), result.InputTokens)
	require.Equal(t, int64(21), result.OutputTokens)
	require.Equal(t, int64(10), result.CacheReadInputTokens)
	require.Equal(t, int64(4), result.ReasoningTokens)

	body := rec.Body.String()
	require.Contains(t, body, `"cache_read_input_tokens":10`)
	require.Contains(t, body, `"output_tokens":21`)
}

func TestGeminiSSEToOpenAISSE_StreamResultIncludesCacheAndReasoning(t *testing.T) {
	t.Parallel()

	sseInput := strings.Join([]string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":42,"cachedContentTokenCount":10,"candidatesTokenCount":17,"thoughtsTokenCount":4}}`,
		``,
	}, "\n")

	rec := httptest.NewRecorder()
	upstream := io.NopCloser(strings.NewReader(sseInput))

	result, err := GeminiSSEToOpenAISSE(rec, upstream, "gemini-2.5-pro")
	require.NoError(t, err)
	require.Equal(t, int64(32), result.InputTokens)
	require.Equal(t, int64(21), result.OutputTokens)
	require.Equal(t, int64(10), result.CacheReadInputTokens)
	require.Equal(t, int64(4), result.ReasoningTokens)
}

func extractGeminiResponseCompletedEvent(t *testing.T, sseBody string) map[string]interface{} {
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
