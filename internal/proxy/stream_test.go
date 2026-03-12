package proxy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractTokens_CacheFields(t *testing.T) {
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
