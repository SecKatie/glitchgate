package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/seckatie/glitchgate/internal/config"
	"github.com/stretchr/testify/require"
)

func float64ptr(v float64) *float64 {
	return &v
}

func TestNewProviderRegistryBuildsProvidersPricingAndAliases(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "chatgpt-pro", Type: "openai", AuthMode: "api_key", MonthlySubscriptionCost: float64ptr(20)},
			{Name: "segment", Type: "openai", BaseURL: "https://api.synthetic.new/v1"},
			{Name: "gemini-api", Type: "gemini", AuthMode: "api_key", APIKey: "test-gemini-key"},
			{Name: "copilot", Type: "github_copilot"},
		},
		ModelList: []config.ModelMapping{
			{
				ModelName:     "chatgpt-5",
				Provider:      "chatgpt-pro",
				UpstreamModel: "gpt-5.4",
				Metadata: &config.ModelMetadata{
					InputTokenCost:  9,
					OutputTokenCost: 19,
					CacheReadCost:   4,
					CacheWriteCost:  7,
				},
			},
		},
	}

	registry, err := NewProviderRegistry(cfg, 42*time.Second)
	require.NoError(t, err)

	providers := registry.Providers()
	require.Len(t, providers, 4)
	require.Equal(t, "openai", providers["chatgpt-pro"].APIFormat())
	require.Equal(t, "openai", providers["segment"].APIFormat())
	require.Equal(t, "gemini", providers["gemini-api"].APIFormat())
	require.Equal(t, "openai", providers["copilot"].APIFormat())

	providerNames := registry.ProviderNames()
	require.Equal(t, "chatgpt-pro", providerNames["chatgpt-pro"])
	require.Equal(t, "chatgpt-pro", providerNames["openai:api.openai.com"])
	require.Equal(t, "segment", providerNames["openai:api.synthetic.new"])
	require.Equal(t, "gemini-api", providerNames["gemini"])
	require.Equal(t, "copilot", providerNames["github_copilot:api.githubcopilot.com"])

	subscriptions := registry.ProviderMonthlySubscriptions()
	require.Equal(t, 20.0, subscriptions["chatgpt-pro"])
	_, ok := subscriptions["segment"]
	require.False(t, ok)

	calc := registry.Calculator()
	override, ok := calc.Lookup("chatgpt-pro", "gpt-5.4")
	require.True(t, ok)
	require.Equal(t, 9.0, override.InputPerMillion)
	require.Equal(t, 19.0, override.OutputPerMillion)
	require.Equal(t, 4.0, override.CacheReadPerMillion)
	require.Equal(t, 7.0, override.CacheWritePerMillion)

	synthetic, ok := calc.Lookup("segment", "hf:MiniMaxAI/MiniMax-M2.5")
	require.True(t, ok)
	require.Equal(t, 0.40, synthetic.InputPerMillion)

	geminiFlash, ok := calc.Lookup("gemini-api", "gemini-2.5-flash")
	require.True(t, ok)
	require.Equal(t, 0.30, geminiFlash.InputPerMillion)
	require.Equal(t, 2.50, geminiFlash.OutputPerMillion)

	copilot, ok := calc.Lookup("copilot", "gpt-5.4")
	require.True(t, ok)
	require.Equal(t, 2.50, copilot.InputPerMillion)
	require.Equal(t, 15.00, copilot.OutputPerMillion)
}

func TestNewProviderRegistryDropsAmbiguousLegacyAlias(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "openai-a", Type: "openai"},
			{Name: "openai-b", Type: "openai"},
		},
	}

	registry, err := NewProviderRegistry(cfg, time.Minute)
	require.NoError(t, err)

	providerNames := registry.ProviderNames()
	require.Equal(t, "openai-a", providerNames["openai-a"])
	require.Equal(t, "openai-b", providerNames["openai-b"])
	_, ok := providerNames["openai:api.openai.com"]
	require.False(t, ok)
}

func TestBootstrapBuildsRuntime(t *testing.T) {
	cfg := &config.Config{
		DatabasePath: filepath.Join(t.TempDir(), "glitchgate.db"),
		Timezone:     "Not/A_Real_Zone",
		Providers: []config.ProviderConfig{
			{Name: "chatgpt-pro", Type: "openai"},
		},
		ModelList: []config.ModelMapping{
			{ModelName: "chatgpt-5", Provider: "chatgpt-pro", UpstreamModel: "gpt-5.4"},
		},
	}

	runtime, err := Bootstrap(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, runtime.Close())
	})

	require.NotNil(t, runtime.Store)
	require.NotNil(t, runtime.AsyncLogger)
	require.NotNil(t, runtime.Calculator)
	require.Equal(t, time.UTC, runtime.Timezone)
	require.Equal(t, "chatgpt-pro", runtime.ProviderNames["openai:api.openai.com"])
	require.Empty(t, runtime.ProviderMonthlySubscriptions)

	entry, ok := runtime.Calculator.Lookup("chatgpt-pro", "gpt-4o")
	require.True(t, ok)
	require.Equal(t, 2.50, entry.InputPerMillion)
}
