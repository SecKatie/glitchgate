package pricing_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"codeberg.org/kglitchy/glitchgate/internal/pricing"
)

func TestIsOfficialOpenAIURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    bool
	}{
		{
			name:    "official api host",
			baseURL: "https://api.openai.com",
			want:    true,
		},
		{
			name:    "chatgpt codex backend",
			baseURL: "https://chatgpt.com/backend-api/codex",
			want:    true,
		},
		{
			name:    "chatgpt codex backend nested path",
			baseURL: "https://chatgpt.com/backend-api/codex/responses",
			want:    true,
		},
		{
			name:    "chatgpt root is not official priced endpoint",
			baseURL: "https://chatgpt.com",
			want:    false,
		},
		{
			name:    "azure openai is not official openai pricing",
			baseURL: "https://my-instance.openai.azure.com",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, pricing.IsOfficialOpenAIURL(tt.baseURL))
		})
	}
}

func TestOpenAIDefaultsIncludesGPT54Pricing(t *testing.T) {
	entry, ok := pricing.OpenAIDefaults["gpt-5.4"]
	require.True(t, ok)
	require.Equal(t, 2.50, entry.InputPerMillion)
	require.Equal(t, 15.00, entry.OutputPerMillion)
	require.Equal(t, 0.25, entry.CacheReadPerMillion)
}
