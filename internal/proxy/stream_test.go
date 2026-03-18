package proxy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractTokens_CacheFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		data              string
		wantInput         int64
		wantOutput        int64
		wantCacheCreation int64
		wantCacheRead     int64
	}{
		{
			name:              "message_start with cache tokens",
			data:              `{"type":"message_start","message":{"id":"msg_01","usage":{"input_tokens":10,"cache_creation_input_tokens":173,"cache_read_input_tokens":57686,"output_tokens":0}}}`,
			wantInput:         10,
			wantCacheCreation: 173,
			wantCacheRead:     57686,
		},
		{
			name:      "message_start without cache tokens",
			data:      `{"type":"message_start","message":{"id":"msg_02","usage":{"input_tokens":42,"output_tokens":0}}}`,
			wantInput: 42,
		},
		{
			name:       "message_delta with output tokens",
			data:       `{"type":"message_delta","usage":{"output_tokens":99}}`,
			wantOutput: 99,
		},
		{
			name:              "message_delta with cache tokens",
			data:              `{"type":"message_delta","usage":{"input_tokens":2258,"output_tokens":94,"cache_creation_input_tokens":0,"cache_read_input_tokens":29056}}`,
			wantInput:         2258,
			wantOutput:        94,
			wantCacheCreation: 0,
			wantCacheRead:     29056,
		},
		{
			name: "unrelated event type is ignored",
			data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var input, output, cacheCreation, cacheRead int64
			extractTokens(tc.data, &input, &output, &cacheCreation, &cacheRead)
			require.Equal(t, tc.wantInput, input, "input tokens")
			require.Equal(t, tc.wantOutput, output, "output tokens")
			require.Equal(t, tc.wantCacheCreation, cacheCreation, "cache creation tokens")
			require.Equal(t, tc.wantCacheRead, cacheRead, "cache read tokens")
		})
	}
}

func TestExtractResponsesTokens_CacheFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		data          string
		wantInput     int64
		wantOutput    int64
		wantCacheRead int64
		wantReasoning int64
	}{
		{
			name:          "response completed with cached tokens",
			data:          `{"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":50,"input_tokens_details":{"cached_tokens":30}}}}`,
			wantInput:     70,
			wantOutput:    50,
			wantCacheRead: 30,
		},
		{
			name:       "response completed without cached tokens",
			data:       `{"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":50}}}`,
			wantInput:  100,
			wantOutput: 50,
		},
		{
			name:          "response completed with reasoning tokens",
			data:          `{"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":200,"output_tokens_details":{"reasoning_tokens":150}}}}`,
			wantInput:     100,
			wantOutput:    200,
			wantReasoning: 150,
		},
		{
			name:          "response completed with all token details",
			data:          `{"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":200,"input_tokens_details":{"cached_tokens":40},"output_tokens_details":{"reasoning_tokens":120}}}}`,
			wantInput:     60,
			wantOutput:    200,
			wantCacheRead: 40,
			wantReasoning: 120,
		},
		{
			name: "unrelated responses event is ignored",
			data: `{"type":"response.output_text.delta","delta":"hello"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var input, output, cacheRead, reasoning int64
			extractResponsesTokens(tc.data, &input, &output, &cacheRead, &reasoning)
			require.Equal(t, tc.wantInput, input, "input tokens")
			require.Equal(t, tc.wantOutput, output, "output tokens")
			require.Equal(t, tc.wantCacheRead, cacheRead, "cache read tokens")
			require.Equal(t, tc.wantReasoning, reasoning, "reasoning tokens")
		})
	}
}
