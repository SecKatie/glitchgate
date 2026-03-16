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
		name         string
		model        string
		wantProvider string
		wantUpstream string
		wantErr      string
	}{
		{
			name:         "exact match",
			model:        "claude-sonnet",
			wantProvider: "anthropic",
			wantUpstream: "claude-sonnet-4-20250514",
		},
		{
			name:         "wildcard match strips prefix",
			model:        "claude_max/claude-opus-4-20250514",
			wantProvider: "claude-max",
			wantUpstream: "claude-opus-4-20250514",
		},
		{
			name:         "exact match takes priority over wildcard",
			model:        "claude_max/claude-sonnet-4-20250514",
			wantProvider: "anthropic-override",
			wantUpstream: "claude-sonnet-4-20250514",
		},
		{
			name:         "different wildcard prefix",
			model:        "other/some-model",
			wantProvider: "other-provider",
			wantUpstream: "some-model",
		},
		{
			name:         "nested slashes preserved in suffix",
			model:        "claude_max/org/model-name",
			wantProvider: "claude-max",
			wantUpstream: "org/model-name",
		},
		{
			name:    "empty suffix returns error",
			model:   "claude_max/",
			wantErr: "model not found",
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
			chain, err := cfg.FindModel(tt.model)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.NotEmpty(t, chain)
			result := chain[0]
			require.Equal(t, tt.wantProvider, result.Provider)
			require.Equal(t, tt.wantUpstream, result.UpstreamModel)
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

	chain, err := cfg.FindModel("prefix/some-model")
	require.NoError(t, err)
	require.NotEmpty(t, chain)
	require.Equal(t, "first", chain[0].Provider, "first wildcard in config order should win")
}

func TestFindModelNonWildcardTrailingSlash(t *testing.T) {
	cfg := &Config{
		ModelList: []ModelMapping{
			{ModelName: "claude_max/", Provider: "exact-slash", UpstreamModel: "something"},
		},
	}

	// "claude_max/" without "*" is an exact match entry, not a wildcard
	chain, err := cfg.FindModel("claude_max/")
	require.NoError(t, err)
	require.NotEmpty(t, chain)
	require.Equal(t, "exact-slash", chain[0].Provider)

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

func TestProviderMonthlySubscriptionCostLoadsFromConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	require.NoError(t, os.WriteFile(cfgPath, []byte(`
master_key: "test"
providers:
  - name: "chatgpt-pro"
    type: "openai"
    auth_mode: "proxy_key"
    api_key: "sk-test"
    monthly_subscription_cost: 20
`), 0o600))

	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	require.Len(t, cfg.Providers, 1)
	require.NotNil(t, cfg.Providers[0].MonthlySubscriptionCost)
	require.Equal(t, 20.0, *cfg.Providers[0].MonthlySubscriptionCost)
}

func TestProviderNamesMustBeUnique(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	require.NoError(t, os.WriteFile(cfgPath, []byte(`
master_key: "test"
providers:
  - name: "dup"
    type: "anthropic"
    base_url: "https://api.anthropic.com"
    auth_mode: "proxy_key"
    api_key: "sk-test"
  - name: "dup"
    type: "openai"
    base_url: "https://api.openai.com"
    auth_mode: "proxy_key"
    api_key: "sk-test"
`), 0o600))

	_, err := Load(cfgPath)
	require.Error(t, err)
	require.Contains(t, err.Error(), `duplicate provider name "dup"`)
}

// --- Virtual model / fallback chain tests (T012) ---

func TestModelMappingValidation_MutualExclusivity(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "both provider and fallbacks rejected",
			yaml: `
master_key: "test"
model_list:
  - model_name: "bad-model"
    provider: "anthropic"
    upstream_model: "claude-3"
    fallbacks: ["other"]
  - model_name: "other"
    provider: "anthropic"
    upstream_model: "claude-3"
`,
			wantErr: "cannot set both provider/upstream_model and fallbacks",
		},
		{
			name: "neither provider nor fallbacks rejected",
			yaml: `
master_key: "test"
model_list:
  - model_name: "empty-model"
`,
			wantErr: "must have either provider+upstream_model or fallbacks",
		},
		{
			name: "direct entry valid",
			yaml: `
master_key: "test"
model_list:
  - model_name: "good-model"
    provider: "anthropic"
    upstream_model: "claude-3"
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.yaml")
			require.NoError(t, os.WriteFile(cfgPath, []byte(tt.yaml), 0o600))
			_, err := Load(cfgPath)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestModelMappingValidation_UnknownFallbackReference(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
master_key: "test"
model_list:
  - model_name: "virtual"
    fallbacks: ["nonexistent"]
`), 0o600))

	_, err := Load(cfgPath)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nonexistent")
	require.Contains(t, err.Error(), "not defined")
}

func TestModelMappingValidation_CycleDetected(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "self-cycle",
			yaml: `
master_key: "test"
model_list:
  - model_name: "a"
    fallbacks: ["a"]
`,
		},
		{
			name: "two-node cycle",
			yaml: `
master_key: "test"
model_list:
  - model_name: "a"
    fallbacks: ["b"]
  - model_name: "b"
    fallbacks: ["a"]
`,
		},
		{
			name: "three-node cycle",
			yaml: `
master_key: "test"
model_list:
  - model_name: "a"
    fallbacks: ["b"]
  - model_name: "b"
    fallbacks: ["c"]
  - model_name: "c"
    fallbacks: ["a"]
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.yaml")
			require.NoError(t, os.WriteFile(cfgPath, []byte(tt.yaml), 0o600))
			_, err := Load(cfgPath)
			require.Error(t, err)
			require.Contains(t, err.Error(), "cycle")
		})
	}
}

func TestFindModel_VirtualChainFlattening(t *testing.T) {
	tests := []struct {
		name          string
		yaml          string
		queryModel    string
		wantProviders []string // expected Provider fields in order
	}{
		{
			name: "single-entry chain (direct model)",
			yaml: `
master_key: "test"
model_list:
  - model_name: "direct"
    provider: "p1"
    upstream_model: "m1"
`,
			queryModel:    "direct",
			wantProviders: []string{"p1"},
		},
		{
			name: "virtual with two direct fallbacks",
			yaml: `
master_key: "test"
model_list:
  - model_name: "virtual"
    fallbacks: ["primary", "secondary"]
  - model_name: "primary"
    provider: "p1"
    upstream_model: "m1"
  - model_name: "secondary"
    provider: "p2"
    upstream_model: "m2"
`,
			queryModel:    "virtual",
			wantProviders: []string{"p1", "p2"},
		},
		{
			name: "nested virtual flattened in order",
			yaml: `
master_key: "test"
model_list:
  - model_name: "outer"
    fallbacks: ["inner", "leaf3"]
  - model_name: "inner"
    fallbacks: ["leaf1", "leaf2"]
  - model_name: "leaf1"
    provider: "p1"
    upstream_model: "m1"
  - model_name: "leaf2"
    provider: "p2"
    upstream_model: "m2"
  - model_name: "leaf3"
    provider: "p3"
    upstream_model: "m3"
`,
			queryModel:    "outer",
			wantProviders: []string{"p1", "p2", "p3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.yaml")
			require.NoError(t, os.WriteFile(cfgPath, []byte(tt.yaml), 0o600))
			cfg, err := Load(cfgPath)
			require.NoError(t, err)

			chain, err := cfg.FindModel(tt.queryModel)
			require.NoError(t, err)
			require.Len(t, chain, len(tt.wantProviders))
			for i, wantProv := range tt.wantProviders {
				require.Equal(t, wantProv, chain[i].Provider, "entry %d", i)
			}
		})
	}
}

func TestFindModel_DirectAndVirtualReturnCorrectSlices(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
master_key: "test"
model_list:
  - model_name: "direct"
    provider: "anthropic"
    upstream_model: "claude-3"
  - model_name: "virtual"
    fallbacks: ["direct"]
`), 0o600))

	cfg, err := Load(cfgPath)
	require.NoError(t, err)

	// Direct entry → slice of one.
	chain, err := cfg.FindModel("direct")
	require.NoError(t, err)
	require.Len(t, chain, 1)
	require.Equal(t, "anthropic", chain[0].Provider)
	require.Equal(t, "claude-3", chain[0].UpstreamModel)

	// Virtual entry → flattened to same single direct entry.
	chain, err = cfg.FindModel("virtual")
	require.NoError(t, err)
	require.Len(t, chain, 1)
	require.Equal(t, "anthropic", chain[0].Provider)
}

func TestFindModel_WildcardFallbacks(t *testing.T) {
	tests := []struct {
		name        string
		yaml        string
		queryModel  string
		wantChain   []struct{ provider, upstream string }
		wantLoadErr string
	}{
		{
			name: "wildcard-resolved fallbacks",
			yaml: `
master_key: "test"
model_list:
  - model_name: "claude-sonnet"
    fallbacks: ["gc/claude-sonnet-4-6", "cm/claude-sonnet-4-6"]
  - model_name: "gc/*"
    provider: "github-copilot"
  - model_name: "cm/*"
    provider: "claude-max"
`,
			queryModel: "claude-sonnet",
			wantChain: []struct{ provider, upstream string }{
				{"github-copilot", "claude-sonnet-4-6"},
				{"claude-max", "claude-sonnet-4-6"},
			},
		},
		{
			name: "mixed explicit and wildcard fallbacks",
			yaml: `
master_key: "test"
model_list:
  - model_name: "virtual"
    fallbacks: ["explicit-entry", "gc/claude-haiku-4"]
  - model_name: "explicit-entry"
    provider: "anthropic"
    upstream_model: "claude-haiku-4-20250514"
  - model_name: "gc/*"
    provider: "github-copilot"
`,
			queryModel: "virtual",
			wantChain: []struct{ provider, upstream string }{
				{"anthropic", "claude-haiku-4-20250514"},
				{"github-copilot", "claude-haiku-4"},
			},
		},
		{
			name: "unknown prefix rejected at load",
			yaml: `
master_key: "test"
model_list:
  - model_name: "virtual"
    fallbacks: ["unknown/claude-sonnet-4-6"]
  - model_name: "gc/*"
    provider: "github-copilot"
`,
			wantLoadErr: "not defined in model_list",
		},
		{
			name: "empty suffix rejected at load",
			yaml: `
master_key: "test"
model_list:
  - model_name: "virtual"
    fallbacks: ["gc/"]
  - model_name: "gc/*"
    provider: "github-copilot"
`,
			wantLoadErr: "not defined in model_list",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.yaml")
			require.NoError(t, os.WriteFile(cfgPath, []byte(tt.yaml), 0o600))

			cfg, err := Load(cfgPath)
			if tt.wantLoadErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantLoadErr)
				return
			}
			require.NoError(t, err)

			chain, err := cfg.FindModel(tt.queryModel)
			require.NoError(t, err)
			require.Len(t, chain, len(tt.wantChain))
			for i, want := range tt.wantChain {
				require.Equal(t, want.provider, chain[i].Provider, "entry %d provider", i)
				require.Equal(t, want.upstream, chain[i].UpstreamModel, "entry %d upstream", i)
			}
		})
	}
}
