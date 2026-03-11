package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExpandTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare tilde", "~", home},
		{"tilde slash", "~/data/proxy.db", filepath.Join(home, "data/proxy.db")},
		{"absolute path unchanged", "/var/lib/proxy.db", "/var/lib/proxy.db"},
		{"relative path unchanged", "proxy.db", "proxy.db"},
		{"tilde in middle unchanged", "/home/~/proxy.db", "/home/~/proxy.db"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, expandTilde(tt.in))
		})
	}
}

func TestFindModelWildcard(t *testing.T) {
	cfg := &Config{
		ModelList: []ModelMapping{
			{ModelName: "claude-sonnet", Provider: "anthropic", UpstreamModel: "claude-sonnet-4-20250514"},
			{ModelName: "claude_max/claude-sonnet-4-20250514", Provider: "anthropic-override", UpstreamModel: "claude-sonnet-4-20250514"},
			{ModelName: "claude_max/*", Provider: "claude-max"},
			{ModelName: "other/*", Provider: "other-provider"},
		},
	}

	tests := []struct {
		name          string
		model         string
		wantProvider  string
		wantUpstream  string
		wantModelName string
		wantErr       string
	}{
		{
			name:          "exact match",
			model:         "claude-sonnet",
			wantProvider:  "anthropic",
			wantUpstream:  "claude-sonnet-4-20250514",
			wantModelName: "claude-sonnet",
		},
		{
			name:          "wildcard match strips prefix",
			model:         "claude_max/claude-opus-4-20250514",
			wantProvider:  "claude-max",
			wantUpstream:  "claude-opus-4-20250514",
			wantModelName: "claude_max/claude-opus-4-20250514",
		},
		{
			name:          "exact match takes priority over wildcard",
			model:         "claude_max/claude-sonnet-4-20250514",
			wantProvider:  "anthropic-override",
			wantUpstream:  "claude-sonnet-4-20250514",
			wantModelName: "claude_max/claude-sonnet-4-20250514",
		},
		{
			name:          "different wildcard prefix",
			model:         "other/some-model",
			wantProvider:  "other-provider",
			wantUpstream:  "some-model",
			wantModelName: "other/some-model",
		},
		{
			name:          "nested slashes preserved in suffix",
			model:         "claude_max/org/model-name",
			wantProvider:  "claude-max",
			wantUpstream:  "org/model-name",
			wantModelName: "claude_max/org/model-name",
		},
		{
			name:    "empty suffix returns error",
			model:   "claude_max/",
			wantErr: "invalid model name",
		},
		{
			name:    "no match returns error",
			model:   "unknown-model",
			wantErr: "model not found",
		},
		{
			name:    "partial prefix does not match",
			model:   "claude_max_extra/something",
			wantErr: "model not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := cfg.FindModel(tt.model)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantProvider, result.Provider)
			require.Equal(t, tt.wantUpstream, result.UpstreamModel)
			require.Equal(t, tt.wantModelName, result.ModelName)
		})
	}
}

func TestFindModelWildcardFirstMatchWins(t *testing.T) {
	cfg := &Config{
		ModelList: []ModelMapping{
			{ModelName: "prefix/*", Provider: "first"},
			{ModelName: "prefix/*", Provider: "second"},
		},
	}

	result, err := cfg.FindModel("prefix/some-model")
	require.NoError(t, err)
	require.Equal(t, "first", result.Provider, "first wildcard in config order should win")
}

func TestFindModelNonWildcardTrailingSlash(t *testing.T) {
	cfg := &Config{
		ModelList: []ModelMapping{
			{ModelName: "claude_max/", Provider: "exact-slash", UpstreamModel: "something"},
		},
	}

	// "claude_max/" without "*" is an exact match entry, not a wildcard
	result, err := cfg.FindModel("claude_max/")
	require.NoError(t, err)
	require.Equal(t, "exact-slash", result.Provider)

	// Should NOT match wildcard-style
	_, err = cfg.FindModel("claude_max/some-model")
	require.Error(t, err)
	require.Contains(t, err.Error(), "model not found")
}

func TestProviderTypeDefaultsToAnthropic(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	require.NoError(t, os.WriteFile(cfgPath, []byte(`
master_key: "test"
providers:
  - name: "default-type"
    base_url: "https://api.anthropic.com"
    auth_mode: "proxy_key"
    api_key: "sk-test"
  - name: "explicit-type"
    type: "anthropic"
    base_url: "https://api.anthropic.com"
    auth_mode: "proxy_key"
    api_key: "sk-test"
`), 0o600))

	cfg, err := Load(cfgPath)
	require.NoError(t, err)

	require.Len(t, cfg.Providers, 2)
	require.Equal(t, "anthropic", cfg.Providers[0].Type, "omitted type should default to anthropic")
	require.Equal(t, "anthropic", cfg.Providers[1].Type, "explicit type should be preserved")
}
