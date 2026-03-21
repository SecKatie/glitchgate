package pricing_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/pricing"
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

func TestProviderURLClassifiers(t *testing.T) {
	t.Run("official anthropic", func(t *testing.T) {
		require.True(t, pricing.IsOfficialAnthropicURL("https://api.anthropic.com/v1/messages"))
		require.False(t, pricing.IsOfficialAnthropicURL("https://example.com"))
		require.False(t, pricing.IsOfficialAnthropicURL("://bad url"))
	})

	t.Run("chutes", func(t *testing.T) {
		require.True(t, pricing.IsChutesURL("https://llm.chutes.ai/v1/chat/completions"))
		require.False(t, pricing.IsChutesURL("https://api.openai.com/v1"))
	})

	t.Run("synthetic", func(t *testing.T) {
		require.True(t, pricing.IsSyntheticURL("https://api.synthetic.new/v1/responses"))
		require.False(t, pricing.IsSyntheticURL(""))
	})
}

func TestProviderKey(t *testing.T) {
	tests := []struct {
		name         string
		providerType string
		baseURL      string
		want         string
	}{
		{
			name:         "includes hostname when present",
			providerType: "openai",
			baseURL:      "https://api.openai.com/v1",
			want:         "openai:api.openai.com",
		},
		{
			name:         "preserves explicit portless hostname",
			providerType: "anthropic",
			baseURL:      "https://api.anthropic.com",
			want:         "anthropic:api.anthropic.com",
		},
		{
			name:         "returns provider type when url is invalid",
			providerType: "github_copilot",
			baseURL:      "://bad url",
			want:         "github_copilot",
		},
		{
			name:         "returns provider type when base url empty",
			providerType: "gemini",
			baseURL:      "",
			want:         "gemini",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, pricing.ProviderKey(tt.providerType, tt.baseURL))
		})
	}
}
