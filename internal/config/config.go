// SPDX-License-Identifier: AGPL-3.0-or-later

// Package config handles loading and validating application configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Config holds the top-level application configuration.
type Config struct {
	MasterKey    string           `mapstructure:"master_key"    yaml:"master_key"`
	Listen       string           `mapstructure:"listen"        yaml:"listen"`
	DatabasePath string           `mapstructure:"database_path" yaml:"database_path"`
	LogPath      string           `mapstructure:"log_path"      yaml:"log_path"` // Path to log file; default "glitchgate.log"
	Timezone     string           `mapstructure:"timezone"      yaml:"timezone"` // IANA timezone name, e.g. "America/New_York"
	Providers    []ProviderConfig `mapstructure:"providers"  yaml:"providers"`
	ModelList    []ModelMapping   `mapstructure:"model_list" yaml:"model_list"`
	OIDC         *OIDCConfig      `mapstructure:"oidc"       yaml:"oidc"`

	// resolvedChains is populated at Load time. It maps every non-wildcard model
	// name to its ordered dispatch slice (one entry for direct models, multiple
	// entries for virtual/fallback models). Wildcard entries are NOT stored here;
	// FindModel falls through to the wildcard scan for those.
	resolvedChains map[string][]ModelMapping
}

// OIDCConfig holds the OIDC provider configuration.
type OIDCConfig struct {
	IssuerURL    string   `mapstructure:"issuer_url"    yaml:"issuer_url"`
	ClientID     string   `mapstructure:"client_id"     yaml:"client_id"`
	ClientSecret string   `mapstructure:"client_secret" yaml:"client_secret"`
	RedirectURL  string   `mapstructure:"redirect_url"  yaml:"redirect_url"`
	Scopes       []string `mapstructure:"scopes"        yaml:"scopes"`
}

// OIDCEnabled returns true when a complete OIDC configuration is present.
func (c *Config) OIDCEnabled() bool {
	return c.OIDC != nil && c.OIDC.IssuerURL != "" && c.OIDC.ClientID != "" && c.OIDC.ClientSecret != ""
}

// ProviderConfig describes an upstream LLM provider endpoint.
type ProviderConfig struct {
	Name           string `mapstructure:"name"            yaml:"name"`
	Type           string `mapstructure:"type"            yaml:"type"` // "anthropic" (default), "github_copilot", "openai", "openai_responses"
	BaseURL        string `mapstructure:"base_url"        yaml:"base_url"`
	AuthMode       string `mapstructure:"auth_mode"       yaml:"auth_mode"` // "proxy_key" or "forward"
	APIKey         string `mapstructure:"api_key"         yaml:"api_key"`
	DefaultVersion string `mapstructure:"default_version" yaml:"default_version"`
	TokenDir       string `mapstructure:"token_dir"       yaml:"token_dir"`        // github_copilot: OAuth token storage directory
	Stream         *bool  `mapstructure:"stream"          yaml:"stream,omitempty"` // nil = follow client; false = force non-streaming upstream
}

// ModelMetadata holds optional per-request pricing rates for a model entry.
// Rates are in USD per million tokens. When present, these override any
// built-in defaults.
type ModelMetadata struct {
	InputTokenCost  float64 `mapstructure:"input_token_cost"  yaml:"input_token_cost"`
	OutputTokenCost float64 `mapstructure:"output_token_cost" yaml:"output_token_cost"`
	CacheReadCost   float64 `mapstructure:"cache_read_cost"   yaml:"cache_read_cost"`
	CacheWriteCost  float64 `mapstructure:"cache_write_cost"  yaml:"cache_write_cost"`
}

// ModelMapping maps a client-facing model name to an upstream provider and model.
// A direct entry has Provider + UpstreamModel set; Fallbacks must be empty.
// A virtual entry has Fallbacks set; Provider and UpstreamModel must be empty.
type ModelMapping struct {
	ModelName     string         `mapstructure:"model_name"     yaml:"model_name"`
	Provider      string         `mapstructure:"provider"       yaml:"provider"`
	UpstreamModel string         `mapstructure:"upstream_model" yaml:"upstream_model"`
	Fallbacks     []string       `mapstructure:"fallbacks"      yaml:"fallbacks"`
	Metadata      *ModelMetadata `mapstructure:"metadata"       yaml:"metadata"`
}

// Load reads the configuration from file, environment, and defaults.
// If configFile is non-empty it is used directly; otherwise the standard
// search paths are tried.  It returns an error if the master_key is not set.
func Load(configFile string) (*Config, error) {
	v := viper.New()

	v.SetConfigType("yaml")

	if configFile != "" {
		v.SetConfigFile(configFile)
	} else {
		v.SetConfigName("config")

		// Search paths in priority order.
		home, err := os.UserHomeDir()
		if err == nil {
			v.AddConfigPath(home + "/.config/glitchgate")
		}
		v.AddConfigPath(".")
		v.AddConfigPath("/etc/glitchgate")
	}

	// Environment variable support.
	v.SetEnvPrefix("GLITCHGATE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Defaults.
	v.SetDefault("listen", ":4000")
	v.SetDefault("database_path", "glitchgate.db")
	v.SetDefault("log_path", "glitchgate.log")
	v.SetDefault("timezone", "UTC")
	v.SetDefault("oidc.scopes", []string{"openid", "email", "profile"})

	// Read config file (not an error if none exists).
	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshalling config: %w", err)
	}

	// Expand ~ prefix in database path.
	cfg.DatabasePath = expandTilde(cfg.DatabasePath)
	// Expand ~ prefix in log path.
	cfg.LogPath = expandTilde(cfg.LogPath)

	// Apply provider defaults and expand env vars.
	for i := range cfg.Providers {
		if cfg.Providers[i].Type == "" {
			cfg.Providers[i].Type = "anthropic"
		}
		cfg.Providers[i].APIKey = os.ExpandEnv(cfg.Providers[i].APIKey)
		// Apply default token directory for github_copilot providers.
		if cfg.Providers[i].Type == "github_copilot" {
			cfg.Providers[i].TokenDir = expandTilde(cfg.Providers[i].TokenDir)
		}
	}

	if cfg.MasterKey == "" {
		return nil, errors.New("master_key is required (set in config file or GLITCHGATE_MASTER_KEY env var)")
	}

	if err := validateCopilotProviders(cfg.Providers); err != nil {
		return nil, err
	}

	if err := cfg.buildResolvedChains(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// buildResolvedChains validates the model_list and populates resolvedChains.
// It enforces mutual exclusivity, unknown reference detection, and cycle detection,
// then flattens each virtual model into its concrete dispatch chain.
func (c *Config) buildResolvedChains() error {
	// Build a name → index map for O(1) lookup.
	byName := make(map[string]int, len(c.ModelList))
	for i, m := range c.ModelList {
		// Skip wildcard entries — they are not stored in the map.
		if strings.HasSuffix(m.ModelName, "/*") {
			continue
		}
		byName[m.ModelName] = i
	}

	// resolveWildcard returns a derived ModelMapping if name matches a wildcard
	// entry (e.g. "gc/claude-sonnet-4-6" matching "gc/*"), otherwise nil.
	resolveWildcard := func(name string) *ModelMapping {
		for i := range c.ModelList {
			pattern := c.ModelList[i].ModelName
			if !strings.HasSuffix(pattern, "/*") {
				continue
			}
			prefix := pattern[:len(pattern)-2] // strip "/*"
			if strings.HasPrefix(name, prefix+"/") {
				suffix := name[len(prefix)+1:]
				if suffix == "" {
					return nil
				}
				m := ModelMapping{
					ModelName:     name,
					Provider:      c.ModelList[i].Provider,
					UpstreamModel: suffix,
				}
				return &m
			}
		}
		return nil
	}

	// Pass 1: validate mutual exclusivity for each non-wildcard entry.
	for _, m := range c.ModelList {
		if strings.HasSuffix(m.ModelName, "/*") {
			continue // wildcards are always direct; no fallbacks allowed
		}
		isDirect := m.Provider != "" || m.UpstreamModel != ""
		isVirtual := len(m.Fallbacks) > 0
		if isDirect && isVirtual {
			return fmt.Errorf("model %q: cannot set both provider/upstream_model and fallbacks", m.ModelName)
		}
		if !isDirect && !isVirtual {
			return fmt.Errorf("model %q: must have either provider+upstream_model or fallbacks", m.ModelName)
		}
	}

	// Pass 2: validate that all names in fallbacks[] exist in model_list (either
	// as an explicit non-wildcard entry, or resolvable via a wildcard entry).
	for _, m := range c.ModelList {
		for _, ref := range m.Fallbacks {
			if _, ok := byName[ref]; !ok {
				if resolveWildcard(ref) == nil {
					return fmt.Errorf("model %q: fallback %q is not defined in model_list", m.ModelName, ref)
				}
			}
		}
	}

	// Pass 3: DFS cycle detection over virtual entries.
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	state := make(map[string]int, len(c.ModelList))
	var detectCycle func(name string, path []string) error
	detectCycle = func(name string, path []string) error {
		switch state[name] {
		case visited:
			return nil
		case visiting:
			// Find where the cycle starts in the path.
			for i, n := range path {
				if n == name {
					return fmt.Errorf("cycle detected in fallback chain: %s", strings.Join(append(path[i:], name), " → "))
				}
			}
			return fmt.Errorf("cycle detected involving model %q", name)
		}
		idx, ok := byName[name]
		if !ok {
			return nil // wildcard or already validated; skip
		}
		m := c.ModelList[idx]
		if len(m.Fallbacks) == 0 {
			state[name] = visited
			return nil
		}
		state[name] = visiting
		for _, ref := range m.Fallbacks {
			if err := detectCycle(ref, append(path, name)); err != nil {
				return err
			}
		}
		state[name] = visited
		return nil
	}

	for _, m := range c.ModelList {
		if strings.HasSuffix(m.ModelName, "/*") {
			continue
		}
		if err := detectCycle(m.ModelName, nil); err != nil {
			return err
		}
	}

	// Pass 4: flatten all non-wildcard entries into resolvedChains.
	chains := make(map[string][]ModelMapping, len(c.ModelList))

	var flatten func(name string) ([]ModelMapping, error)
	flatten = func(name string) ([]ModelMapping, error) {
		if chain, ok := chains[name]; ok {
			return chain, nil // already computed
		}
		idx, ok := byName[name]
		if !ok {
			if wm := resolveWildcard(name); wm != nil {
				chain := []ModelMapping{*wm}
				chains[name] = chain
				return chain, nil
			}
			return nil, fmt.Errorf("model %q not found during flattening", name)
		}
		m := c.ModelList[idx]
		if len(m.Fallbacks) == 0 {
			// Direct entry: chain of one.
			chain := []ModelMapping{m}
			chains[name] = chain
			return chain, nil
		}
		// Virtual entry: expand each fallback recursively.
		var chain []ModelMapping
		for _, ref := range m.Fallbacks {
			sub, err := flatten(ref)
			if err != nil {
				return nil, err
			}
			chain = append(chain, sub...)
		}
		chains[name] = chain
		return chain, nil
	}

	for _, m := range c.ModelList {
		if strings.HasSuffix(m.ModelName, "/*") {
			continue
		}
		if _, err := flatten(m.ModelName); err != nil {
			return err
		}
	}

	c.resolvedChains = chains
	return nil
}

// validateCopilotProviders checks that multiple github_copilot providers have
// distinct, non-empty token_dir values so they don't overwrite each other's tokens.
func validateCopilotProviders(providers []ProviderConfig) error {
	var copilots []ProviderConfig
	for _, p := range providers {
		if p.Type == "github_copilot" {
			copilots = append(copilots, p)
		}
	}
	if len(copilots) < 2 {
		return nil
	}
	seen := make(map[string]string) // token_dir → provider name
	for _, p := range copilots {
		if p.TokenDir == "" {
			return fmt.Errorf(
				"provider %q: token_dir is required when multiple github_copilot providers are configured",
				p.Name,
			)
		}
		if other, conflict := seen[p.TokenDir]; conflict {
			return fmt.Errorf(
				"providers %q and %q share the same token_dir %q; each github_copilot provider must have a unique token_dir",
				other, p.Name, p.TokenDir,
			)
		}
		seen[p.TokenDir] = p.Name
	}
	return nil
}

// expandTilde replaces a leading "~" or "~/" with the current user's home
// directory.  All other strings are returned unchanged.
func expandTilde(path string) string {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return home
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

// FindProvider returns the provider with the given name, or an error if not found.
func (c *Config) FindProvider(name string) (*ProviderConfig, error) {
	for i := range c.Providers {
		if c.Providers[i].Name == name {
			return &c.Providers[i], nil
		}
	}
	return nil, fmt.Errorf("provider not found: %s", name)
}

// FindModel resolves a client-facing model name to an ordered dispatch chain.
//
// For direct models it returns a slice of one. For virtual models it returns
// the pre-flattened slice (populated at Load time). Wildcard entries are
// resolved via a scan of ModelList and always return a slice of one.
//
// Resolution order:
//  1. resolvedChains lookup (direct and virtual non-wildcard entries).
//  2. Wildcard scan — entries whose model_name ends in "/*" (direct entries only).
//  3. Exact scan of ModelList (fallback for Config values constructed directly
//     in tests without calling Load, which skips resolvedChains population).
//  4. No match — return error.
func (c *Config) FindModel(modelName string) ([]ModelMapping, error) {
	// Pass 1: pre-computed chains (populated by Load).
	if c.resolvedChains != nil {
		if chain, ok := c.resolvedChains[modelName]; ok {
			return chain, nil
		}
		// Not in map — fall through to wildcard scan below.
	} else {
		// resolvedChains not populated (direct Config construction without Load).
		// Do a linear exact scan so existing unit tests continue to work.
		for i := range c.ModelList {
			if c.ModelList[i].ModelName == modelName {
				return []ModelMapping{c.ModelList[i]}, nil
			}
		}
	}

	// Pass 2: wildcard scan (entries ending in "/*").
	for i := range c.ModelList {
		pattern := c.ModelList[i].ModelName
		if !strings.HasSuffix(pattern, "/*") {
			continue
		}
		prefix := pattern[:len(pattern)-2] // strip "/*"
		if strings.HasPrefix(modelName, prefix+"/") {
			suffix := modelName[len(prefix)+1:]
			if suffix == "" {
				return nil, fmt.Errorf("invalid model name: %s (empty model after prefix)", modelName)
			}
			return []ModelMapping{{
				ModelName:     modelName,
				Provider:      c.ModelList[i].Provider,
				UpstreamModel: suffix,
			}}, nil
		}
	}

	return nil, fmt.Errorf("model not found: %s", modelName)
}
