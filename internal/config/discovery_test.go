package config

import (
	"context"
	"fmt"
	"testing"

	"github.com/seckatie/glitchgate/internal/provider"
	"github.com/stretchr/testify/require"
)

func TestMatchDiscoverFilter(t *testing.T) {
	tests := []struct {
		name    string
		modelID string
		filters []string
		want    bool
		wantErr bool
	}{
		{
			name:    "empty filter includes all",
			modelID: "claude-sonnet-4-6",
			filters: nil,
			want:    true,
		},
		{
			name:    "include pattern matches",
			modelID: "claude-sonnet-4-6",
			filters: []string{"claude-*"},
			want:    true,
		},
		{
			name:    "include pattern does not match",
			modelID: "gpt-4o",
			filters: []string{"claude-*"},
			want:    false,
		},
		{
			name:    "exclude pattern removes match",
			modelID: "claude-sonnet-4-6-preview",
			filters: []string{"*", "!*-preview"},
			want:    false,
		},
		{
			name:    "exclude pattern does not remove non-match",
			modelID: "claude-sonnet-4-6",
			filters: []string{"*", "!*-preview"},
			want:    true,
		},
		{
			name:    "exclude only — model not excluded is included",
			modelID: "claude-sonnet-4-6",
			filters: []string{"!*-preview"},
			want:    true,
		},
		{
			name:    "exclude only — model excluded",
			modelID: "gpt-preview",
			filters: []string{"!*-preview"},
			want:    false,
		},
		{
			name:    "exclude takes precedence over include",
			modelID: "claude-sonnet-4-6-preview",
			filters: []string{"claude-*", "!*-preview"},
			want:    false,
		},
		{
			name:    "multiple include patterns",
			modelID: "gemini-2.0-flash",
			filters: []string{"claude-*", "gemini-*"},
			want:    true,
		},
		{
			name:    "invalid glob pattern returns error",
			modelID: "test",
			filters: []string{"[invalid"},
			wantErr: true,
		},
		{
			name:    "invalid exclude glob pattern returns error",
			modelID: "test",
			filters: []string{"![invalid"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := matchDiscoverFilter(tt.modelID, tt.filters)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// mockDiscoverer is a test double implementing provider.Provider and
// provider.ModelDiscoverer.
type mockDiscoverer struct {
	name   string
	models []provider.DiscoveredModel
	err    error
	called bool
}

func (m *mockDiscoverer) Name() string      { return m.name }
func (m *mockDiscoverer) AuthMode() string  { return "proxy_key" }
func (m *mockDiscoverer) APIFormat() string { return "anthropic" }
func (m *mockDiscoverer) SendRequest(_ context.Context, _ *provider.Request) (*provider.Response, error) {
	return nil, nil
}

func (m *mockDiscoverer) ListModels(_ context.Context) ([]provider.DiscoveredModel, error) {
	m.called = true
	return m.models, m.err
}

func TestInjectDiscoveredModels(t *testing.T) {
	mock := &mockDiscoverer{
		name: "test-provider",
		models: []provider.DiscoveredModel{
			{ID: "model-a", DisplayName: "Model A"},
			{ID: "model-b", DisplayName: "Model B"},
		},
	}

	cfg := &Config{
		Providers: []ProviderConfig{
			{Name: "test-provider", Type: "anthropic", DiscoverModels: true},
		},
	}

	providers := map[string]provider.Provider{
		"test-provider": mock,
	}

	err := cfg.InjectDiscoveredModels(providers)
	require.NoError(t, err)
	require.True(t, mock.called)
	require.Len(t, cfg.ModelList, 2)

	// Verify default prefix is "{name}/"
	require.Equal(t, "test-provider/model-a", cfg.ModelList[0].ModelName)
	require.Equal(t, "test-provider", cfg.ModelList[0].Provider)
	require.Equal(t, "model-a", cfg.ModelList[0].UpstreamModel)
	require.Equal(t, "test-provider/model-b", cfg.ModelList[1].ModelName)

	// Verify FindModel resolves discovered models.
	chain, err := cfg.FindModel("test-provider/model-a")
	require.NoError(t, err)
	require.Len(t, chain, 1)
	require.Equal(t, "model-a", chain[0].UpstreamModel)
}

func TestInjectDiscoveredModels_CustomPrefix(t *testing.T) {
	prefix := "custom/"
	mock := &mockDiscoverer{
		name: "provider1",
		models: []provider.DiscoveredModel{
			{ID: "model-x"},
		},
	}

	cfg := &Config{
		Providers: []ProviderConfig{
			{Name: "provider1", Type: "anthropic", DiscoverModels: true, ModelPrefix: &prefix},
		},
	}

	err := cfg.InjectDiscoveredModels(map[string]provider.Provider{"provider1": mock})
	require.NoError(t, err)
	require.Len(t, cfg.ModelList, 1)
	require.Equal(t, "custom/model-x", cfg.ModelList[0].ModelName)
}

func TestInjectDiscoveredModels_EmptyPrefix(t *testing.T) {
	empty := ""
	mock := &mockDiscoverer{
		name: "provider1",
		models: []provider.DiscoveredModel{
			{ID: "model-x"},
		},
	}

	cfg := &Config{
		Providers: []ProviderConfig{
			{Name: "provider1", Type: "anthropic", DiscoverModels: true, ModelPrefix: &empty},
		},
	}

	err := cfg.InjectDiscoveredModels(map[string]provider.Provider{"provider1": mock})
	require.NoError(t, err)
	require.Len(t, cfg.ModelList, 1)
	require.Equal(t, "model-x", cfg.ModelList[0].ModelName)
}

func TestInjectDiscoveredModels_NilPrefixDefaultsToProviderName(t *testing.T) {
	mock := &mockDiscoverer{
		name: "anthropic",
		models: []provider.DiscoveredModel{
			{ID: "claude-sonnet-4-6"},
		},
	}

	cfg := &Config{
		Providers: []ProviderConfig{
			{Name: "anthropic", Type: "anthropic", DiscoverModels: true},
		},
	}

	err := cfg.InjectDiscoveredModels(map[string]provider.Provider{"anthropic": mock})
	require.NoError(t, err)
	require.Len(t, cfg.ModelList, 1)
	require.Equal(t, "anthropic/claude-sonnet-4-6", cfg.ModelList[0].ModelName)
}

func TestInjectDiscoveredModels_ExplicitPrecedence(t *testing.T) {
	mock := &mockDiscoverer{
		name: "anthropic",
		models: []provider.DiscoveredModel{
			{ID: "claude-sonnet-4-6"},
			{ID: "claude-opus-4-6"},
		},
	}

	cfg := &Config{
		Providers: []ProviderConfig{
			{Name: "anthropic", Type: "anthropic", DiscoverModels: true},
		},
		ModelList: []ModelMapping{
			{
				ModelName:     "anthropic/claude-sonnet-4-6",
				Provider:      "anthropic",
				UpstreamModel: "claude-sonnet-4-6-override",
			},
		},
	}

	err := cfg.InjectDiscoveredModels(map[string]provider.Provider{"anthropic": mock})
	require.NoError(t, err)

	// Explicit entry kept, discovered duplicate skipped, new model added.
	require.Len(t, cfg.ModelList, 2)
	require.Equal(t, "anthropic/claude-sonnet-4-6", cfg.ModelList[0].ModelName)
	require.Equal(t, "claude-sonnet-4-6-override", cfg.ModelList[0].UpstreamModel) // Explicit wins
	require.Equal(t, "anthropic/claude-opus-4-6", cfg.ModelList[1].ModelName)
}

func TestInjectDiscoveredModels_NoDiscoveryProviders(t *testing.T) {
	mock := &mockDiscoverer{
		name: "provider1",
		models: []provider.DiscoveredModel{
			{ID: "model-a"},
		},
	}

	cfg := &Config{
		Providers: []ProviderConfig{
			{Name: "provider1", Type: "anthropic", DiscoverModels: false},
		},
		ModelList: []ModelMapping{
			{ModelName: "existing", Provider: "provider1", UpstreamModel: "existing-model"},
		},
	}

	err := cfg.InjectDiscoveredModels(map[string]provider.Provider{"provider1": mock})
	require.NoError(t, err)
	require.False(t, mock.called)
	require.Len(t, cfg.ModelList, 1)
	require.Equal(t, "existing", cfg.ModelList[0].ModelName)
}

func TestInjectDiscoveredModels_DiscoveryError_Degrades(t *testing.T) {
	mock := &mockDiscoverer{
		name: "provider1",
		err:  fmt.Errorf("network timeout"),
	}

	cfg := &Config{
		Providers: []ProviderConfig{
			{Name: "provider1", Type: "anthropic", DiscoverModels: true},
		},
	}

	err := cfg.InjectDiscoveredModels(map[string]provider.Provider{"provider1": mock})
	require.NoError(t, err)
	require.Empty(t, cfg.ModelList)
}

func TestInjectDiscoveredModels_MixedProviders(t *testing.T) {
	discoveryProvider := &mockDiscoverer{
		name: "provider-a",
		models: []provider.DiscoveredModel{
			{ID: "model-1"},
			{ID: "model-2"},
		},
	}
	nonDiscoveryProvider := &mockDiscoverer{
		name: "provider-b",
		models: []provider.DiscoveredModel{
			{ID: "should-not-appear"},
		},
	}

	cfg := &Config{
		Providers: []ProviderConfig{
			{Name: "provider-a", Type: "anthropic", DiscoverModels: true},
			{Name: "provider-b", Type: "openai", DiscoverModels: false},
		},
		ModelList: []ModelMapping{
			{ModelName: "explicit-model", Provider: "provider-b", UpstreamModel: "explicit-upstream"},
		},
	}

	err := cfg.InjectDiscoveredModels(map[string]provider.Provider{
		"provider-a": discoveryProvider,
		"provider-b": nonDiscoveryProvider,
	})
	require.NoError(t, err)
	require.True(t, discoveryProvider.called)
	require.False(t, nonDiscoveryProvider.called)
	require.Len(t, cfg.ModelList, 3) // 1 explicit + 2 discovered
	require.Equal(t, "explicit-model", cfg.ModelList[0].ModelName)
	require.Equal(t, "provider-a/model-1", cfg.ModelList[1].ModelName)
	require.Equal(t, "provider-a/model-2", cfg.ModelList[2].ModelName)
}

func TestInjectDiscoveredModels_WithFilter(t *testing.T) {
	mock := &mockDiscoverer{
		name: "anthropic",
		models: []provider.DiscoveredModel{
			{ID: "claude-sonnet-4-6"},
			{ID: "claude-sonnet-4-6-preview"},
			{ID: "claude-opus-4-6"},
		},
	}

	cfg := &Config{
		Providers: []ProviderConfig{
			{
				Name:           "anthropic",
				Type:           "anthropic",
				DiscoverModels: true,
				DiscoverFilter: []string{"claude-*", "!*-preview"},
			},
		},
	}

	err := cfg.InjectDiscoveredModels(map[string]provider.Provider{"anthropic": mock})
	require.NoError(t, err)
	require.Len(t, cfg.ModelList, 2)
	require.Equal(t, "anthropic/claude-sonnet-4-6", cfg.ModelList[0].ModelName)
	require.Equal(t, "anthropic/claude-opus-4-6", cfg.ModelList[1].ModelName)
}

func TestValidateDiscoveryProviders(t *testing.T) {
	tests := []struct {
		name      string
		providers []ProviderConfig
		wantErr   bool
		errMsg    string
	}{
		{
			name: "github_copilot with discover_models rejects",
			providers: []ProviderConfig{
				{Name: "copilot", Type: "github_copilot", DiscoverModels: true},
			},
			wantErr: true,
			errMsg:  "discover_models is not supported",
		},
		{
			name: "github_copilot without discover_models succeeds",
			providers: []ProviderConfig{
				{Name: "copilot", Type: "github_copilot", DiscoverModels: false},
			},
			wantErr: false,
		},
		{
			name: "github_copilot with default discover_models succeeds",
			providers: []ProviderConfig{
				{Name: "copilot", Type: "github_copilot"},
			},
			wantErr: false,
		},
		{
			name: "anthropic with discover_models succeeds",
			providers: []ProviderConfig{
				{Name: "anthropic", Type: "anthropic", DiscoverModels: true},
			},
			wantErr: false,
		},
		{
			name: "openai with discover_models succeeds",
			providers: []ProviderConfig{
				{Name: "openai", Type: "openai", DiscoverModels: true},
			},
			wantErr: false,
		},
		{
			name: "gemini with discover_models succeeds",
			providers: []ProviderConfig{
				{Name: "gemini", Type: "gemini", DiscoverModels: true},
			},
			wantErr: false,
		},
		{
			name: "openai_responses with discover_models succeeds",
			providers: []ProviderConfig{
				{Name: "openai-resp", Type: "openai_responses", DiscoverModels: true},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDiscoveryProviders(tt.providers)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
