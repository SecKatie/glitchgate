// SPDX-License-Identifier: AGPL-3.0-or-later

// Package config handles loading and validating application configuration.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds the top-level application configuration.
type Config struct {
	MasterKey                  string           `mapstructure:"master_key"    yaml:"master_key"`
	Listen                     string           `mapstructure:"listen"        yaml:"listen"`
	DatabasePath               string           `mapstructure:"database_path" yaml:"database_path"`
	DatabaseURL                string           `mapstructure:"database_url"  yaml:"database_url"`
	LogPath                    string           `mapstructure:"log_path"      yaml:"log_path"` // Path to log file; default "glitchgate.log"
	Timezone                   string           `mapstructure:"timezone"      yaml:"timezone"` // IANA timezone name, e.g. "America/New_York"
	ProxyMaxBodyBytes          int              `mapstructure:"proxy_max_body_bytes"          yaml:"proxy_max_body_bytes"`
	UpstreamRequestTimeout     time.Duration    `mapstructure:"upstream_request_timeout"      yaml:"upstream_request_timeout"`
	AsyncLogBufferSize         int              `mapstructure:"async_log_buffer_size"         yaml:"async_log_buffer_size"`
	AsyncLogWriteTimeout       time.Duration    `mapstructure:"async_log_write_timeout"       yaml:"async_log_write_timeout"`
	LoginRateLimitPerMinute    int              `mapstructure:"login_rate_limit_per_minute"   yaml:"login_rate_limit_per_minute"`
	LoginRateLimitBurst        int              `mapstructure:"login_rate_limit_burst"        yaml:"login_rate_limit_burst"`
	ProxyRateLimitPerMinute    int              `mapstructure:"proxy_rate_limit_per_minute"   yaml:"proxy_rate_limit_per_minute"`
	ProxyRateLimitBurst        int              `mapstructure:"proxy_rate_limit_burst"        yaml:"proxy_rate_limit_burst"`
	ProxyIPRateLimitPerMinute  int              `mapstructure:"proxy_ip_rate_limit_per_minute" yaml:"proxy_ip_rate_limit_per_minute"`
	ProxyIPRateLimitBurst      int              `mapstructure:"proxy_ip_rate_limit_burst"     yaml:"proxy_ip_rate_limit_burst"`
	RequestLogRetention        time.Duration    `mapstructure:"request_log_retention"         yaml:"request_log_retention"`
	RequestLogPruneInterval    time.Duration    `mapstructure:"request_log_prune_interval"    yaml:"request_log_prune_interval"`
	RequestLogPruneBatchSize   int              `mapstructure:"request_log_prune_batch_size"  yaml:"request_log_prune_batch_size"`
	RequestLogBodyMaxBytes     int              `mapstructure:"request_log_body_max_bytes"    yaml:"request_log_body_max_bytes"`
	Providers                  []ProviderConfig `mapstructure:"providers"  yaml:"providers"`
	ModelList                  []ModelMapping   `mapstructure:"model_list" yaml:"model_list"`
	OIDC                       *OIDCConfig      `mapstructure:"oidc"       yaml:"oidc"`
	MetricsEnabled             bool             `mapstructure:"metrics_enabled" yaml:"metrics_enabled"`
	LogLevel                   string           `mapstructure:"log_level"       yaml:"log_level"`
	CircuitBreakerThreshold    int              `mapstructure:"circuit_breaker_threshold"      yaml:"circuit_breaker_threshold"`
	CircuitBreakerCooldownSecs int              `mapstructure:"circuit_breaker_cooldown_secs"  yaml:"circuit_breaker_cooldown_secs"`

	// resolvedChains is populated at Load time. It maps every non-wildcard model
	// name to its ordered dispatch slice (one entry for direct models, multiple
	// entries for virtual/fallback models). Wildcard entries are NOT stored here;
	// FindModel falls through to the wildcard scan for those.
	resolvedChains map[string][]DispatchTarget
}

// Default operational limits and retention settings used when config values
// are omitted or invalid.
const (
	DefaultProxyMaxBodyBytes          = 4 << 20
	DefaultUpstreamRequestTimeout     = 5 * time.Minute
	DefaultAsyncLogBufferSize         = 1000
	DefaultAsyncLogWriteTimeout       = 5 * time.Second
	DefaultLoginRateLimitPerMinute    = 10
	DefaultLoginRateLimitBurst        = 5
	DefaultProxyRateLimitPerMinute    = 120
	DefaultProxyRateLimitBurst        = 30
	DefaultProxyIPRateLimitPerMinute  = 240
	DefaultProxyIPRateLimitBurst      = 60
	DefaultRequestLogRetention        = 30 * 24 * time.Hour
	DefaultRequestLogPruneInterval    = time.Hour
	DefaultRequestLogPruneBatchSize   = 1000
	DefaultRequestLogBodyMaxBytes     = DefaultProxyMaxBodyBytes
	DefaultCircuitBreakerThreshold    = 5
	DefaultCircuitBreakerCooldownSecs = 30
)

// OIDCConfig holds the OIDC provider configuration.
type OIDCConfig struct {
	IssuerURL    string   `mapstructure:"issuer_url"    yaml:"issuer_url"`
	ClientID     string   `mapstructure:"client_id"     yaml:"client_id"`
	ClientSecret string   `mapstructure:"client_secret" yaml:"client_secret"` //nolint:gosec // struct field for runtime config, not a hardcoded secret
	RedirectURL  string   `mapstructure:"redirect_url"  yaml:"redirect_url"`
	Scopes       []string `mapstructure:"scopes"        yaml:"scopes"`
}

// OIDCEnabled returns true when a complete OIDC configuration is present.
func (c *Config) OIDCEnabled() bool {
	return c.OIDC != nil && c.OIDC.IssuerURL != "" && c.OIDC.ClientID != "" && c.OIDC.ClientSecret != ""
}

// DBBackend returns "postgres" when a database_url is configured, otherwise "sqlite".
func (c *Config) DBBackend() string {
	if c.DatabaseURL != "" {
		return "postgres"
	}
	return "sqlite"
}

// ProviderConfig describes an upstream LLM provider endpoint.
type ProviderConfig struct {
	Name                    string   `mapstructure:"name"                      yaml:"name"`
	Type                    string   `mapstructure:"type"                      yaml:"type"` // "anthropic" (default), "github_copilot", "openai", "openai_responses", "gemini"
	BaseURL                 string   `mapstructure:"base_url"                  yaml:"base_url"`
	AuthMode                string   `mapstructure:"auth_mode"                 yaml:"auth_mode"` // "proxy_key", "forward"; anthropic/gemini also support "vertex"
	APIKey                  string   `mapstructure:"api_key"                   yaml:"api_key"`   //nolint:gosec // struct field for runtime config, not a hardcoded secret
	DefaultVersion          string   `mapstructure:"default_version"           yaml:"default_version"`
	TokenDir                string   `mapstructure:"token_dir"                 yaml:"token_dir"`                           // github_copilot: OAuth token storage directory
	CredentialsFile         string   `mapstructure:"credentials_file"          yaml:"credentials_file"`                    // anthropic/gemini (vertex mode): path to service account JSON; empty = ADC
	Project                 string   `mapstructure:"project"                   yaml:"project"`                             // anthropic/gemini (vertex mode): GCP project ID
	Region                  string   `mapstructure:"region"                    yaml:"region"`                              // anthropic/gemini (vertex mode): GCP region (e.g. "us-east5")
	Stream                  *bool    `mapstructure:"stream"                    yaml:"stream,omitempty"`                    // nil = follow client; false = force non-streaming upstream
	MonthlySubscriptionCost *float64 `mapstructure:"monthly_subscription_cost" yaml:"monthly_subscription_cost,omitempty"` // optional provider-level monthly spend baseline in USD
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

// IsWildcard returns true for wildcard prefix entries (e.g. "chatgpt/*").
func (m ModelMapping) IsWildcard() bool { return strings.HasSuffix(m.ModelName, "/*") }

// IsDirect returns true for concrete model entries with a provider and upstream model.
func (m ModelMapping) IsDirect() bool { return m.Provider != "" && len(m.Fallbacks) == 0 }

// IsVirtual returns true for entries that route through a fallback chain.
func (m ModelMapping) IsVirtual() bool { return len(m.Fallbacks) > 0 }

// WildcardPrefix returns the prefix portion of a wildcard entry (e.g. "chatgpt"
// for "chatgpt/*"), or "" if the entry is not a wildcard.
func (m ModelMapping) WildcardPrefix() string {
	if prefix, ok := strings.CutSuffix(m.ModelName, "/*"); ok {
		return prefix
	}
	return ""
}

// DispatchTarget is a single concrete upstream destination for a proxy request.
// FindModel returns a slice of these — one for direct models, multiple for
// virtual/fallback models. The proxy pipeline only needs Provider + UpstreamModel;
// Metadata is carried through for pricing overrides.
type DispatchTarget struct {
	Provider      string
	UpstreamModel string
	Metadata      *ModelMetadata
}

// MatchesWildcard checks if modelName matches a wildcard prefix (e.g. "chatgpt"
// matches "chatgpt/gpt-5.4") and returns the suffix ("gpt-5.4").
func MatchesWildcard(prefix, modelName string) (suffix string, ok bool) {
	after, found := strings.CutPrefix(modelName, prefix+"/")
	if found && after != "" {
		return after, true
	}
	return "", false
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
	v.SetDefault("database_url", "")
	v.SetDefault("log_path", "glitchgate.log")
	v.SetDefault("timezone", "UTC")
	v.SetDefault("proxy_max_body_bytes", DefaultProxyMaxBodyBytes)
	v.SetDefault("upstream_request_timeout", DefaultUpstreamRequestTimeout)
	v.SetDefault("async_log_buffer_size", DefaultAsyncLogBufferSize)
	v.SetDefault("async_log_write_timeout", DefaultAsyncLogWriteTimeout)
	v.SetDefault("login_rate_limit_per_minute", DefaultLoginRateLimitPerMinute)
	v.SetDefault("login_rate_limit_burst", DefaultLoginRateLimitBurst)
	v.SetDefault("proxy_rate_limit_per_minute", DefaultProxyRateLimitPerMinute)
	v.SetDefault("proxy_rate_limit_burst", DefaultProxyRateLimitBurst)
	v.SetDefault("proxy_ip_rate_limit_per_minute", DefaultProxyIPRateLimitPerMinute)
	v.SetDefault("proxy_ip_rate_limit_burst", DefaultProxyIPRateLimitBurst)
	v.SetDefault("request_log_retention", DefaultRequestLogRetention)
	v.SetDefault("request_log_prune_interval", DefaultRequestLogPruneInterval)
	v.SetDefault("request_log_prune_batch_size", DefaultRequestLogPruneBatchSize)
	v.SetDefault("request_log_body_max_bytes", DefaultRequestLogBodyMaxBytes)
	v.SetDefault("metrics_enabled", true)
	v.SetDefault("log_level", "info")
	v.SetDefault("circuit_breaker_threshold", DefaultCircuitBreakerThreshold)
	v.SetDefault("circuit_breaker_cooldown_secs", DefaultCircuitBreakerCooldownSecs)
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

	// Validate that database_url and a non-default database_path are not both set.
	if cfg.DatabaseURL != "" && cfg.DatabasePath != "glitchgate.db" && cfg.DatabasePath != "" {
		return nil, errors.New("cannot set both database_url and database_path; choose one database backend")
	}

	// Apply provider defaults and expand env vars.
	for i := range cfg.Providers {
		if cfg.Providers[i].Type == "" {
			cfg.Providers[i].Type = "anthropic"
		}
		if cfg.Providers[i].Type == "vertex_gemini" {
			return nil, fmt.Errorf("provider %q: type \"vertex_gemini\" has been removed; use type \"gemini\" with auth_mode \"vertex\" instead", cfg.Providers[i].Name)
		}
		if cfg.Providers[i].Type == "vertex_claude" {
			return nil, fmt.Errorf("provider %q: type \"vertex_claude\" has been removed; use type \"anthropic\" with auth_mode \"vertex\" instead", cfg.Providers[i].Name)
		}
		cfg.Providers[i].APIKey = os.ExpandEnv(cfg.Providers[i].APIKey)
		// Apply default token directory for github_copilot providers.
		if cfg.Providers[i].Type == "github_copilot" {
			cfg.Providers[i].TokenDir = expandTilde(cfg.Providers[i].TokenDir)
		}
		// Expand paths for providers that use credentials files (vertex auth mode).
		if cfg.Providers[i].AuthMode == "vertex" {
			cfg.Providers[i].CredentialsFile = expandTilde(cfg.Providers[i].CredentialsFile)
		}
		// Normalize deprecated gemini auth_mode values.
		if cfg.Providers[i].Type == "gemini" {
			switch cfg.Providers[i].AuthMode {
			case "proxy_key":
				slog.Warn("gemini auth_mode \"proxy_key\" is deprecated, use \"api_key\" instead", "provider", cfg.Providers[i].Name)
				cfg.Providers[i].AuthMode = "api_key"
			case "forward":
				return nil, fmt.Errorf("provider %q: gemini auth_mode \"forward\" is no longer supported; use \"api_key\" or \"vertex\"", cfg.Providers[i].Name)
			}
		}
	}

	if cfg.MasterKey == "" {
		return nil, errors.New("master_key is required (set in config file or GLITCHGATE_MASTER_KEY env var)")
	}

	if err := validateProviderNames(cfg.Providers); err != nil {
		return nil, err
	}

	if err := validateCopilotProviders(cfg.Providers); err != nil {
		return nil, err
	}

	if err := validateVertexAuthProviders(cfg.Providers); err != nil {
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
		if m.IsWildcard() {
			continue
		}
		byName[m.ModelName] = i
	}

	// resolveWildcard returns a derived ModelMapping if name matches a wildcard
	// entry (e.g. "gc/claude-sonnet-4-6" matching "gc/*"), otherwise nil.
	resolveWildcard := func(name string) *ModelMapping {
		for i := range c.ModelList {
			prefix := c.ModelList[i].WildcardPrefix()
			if prefix == "" {
				continue
			}
			if suffix, ok := MatchesWildcard(prefix, name); ok {
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
		if m.IsWildcard() {
			continue
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
		if m.IsWildcard() {
			continue
		}
		if err := detectCycle(m.ModelName, nil); err != nil {
			return err
		}
	}

	// Pass 4: flatten all non-wildcard entries into resolvedChains.
	chains := make(map[string][]DispatchTarget, len(c.ModelList))

	var flatten func(name string) ([]DispatchTarget, error)
	flatten = func(name string) ([]DispatchTarget, error) {
		if chain, ok := chains[name]; ok {
			return chain, nil // already computed
		}
		idx, ok := byName[name]
		if !ok {
			if wm := resolveWildcard(name); wm != nil {
				chain := []DispatchTarget{{
					Provider:      wm.Provider,
					UpstreamModel: wm.UpstreamModel,
					Metadata:      wm.Metadata,
				}}
				chains[name] = chain
				return chain, nil
			}
			return nil, fmt.Errorf("model %q not found during flattening", name)
		}
		m := c.ModelList[idx]
		if len(m.Fallbacks) == 0 {
			// Direct entry: chain of one.
			chain := []DispatchTarget{{
				Provider:      m.Provider,
				UpstreamModel: m.UpstreamModel,
				Metadata:      m.Metadata,
			}}
			chains[name] = chain
			return chain, nil
		}
		// Virtual entry: expand each fallback recursively.
		var chain []DispatchTarget
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
		if m.IsWildcard() {
			continue
		}
		if _, err := flatten(m.ModelName); err != nil {
			return err
		}
	}

	c.resolvedChains = chains
	return nil
}

func validateProviderNames(providers []ProviderConfig) error {
	seen := make(map[string]struct{}, len(providers))
	for _, p := range providers {
		if p.Name == "" {
			return errors.New("provider name is required")
		}
		if _, exists := seen[p.Name]; exists {
			return fmt.Errorf("duplicate provider name %q", p.Name)
		}
		seen[p.Name] = struct{}{}
	}
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

// validateVertexAuthProviders checks that providers using auth_mode "vertex"
// have the required project field and that credentials_file (if set) exists.
// Also validates gemini-specific auth_mode requirements.
func validateVertexAuthProviders(providers []ProviderConfig) error {
	for _, p := range providers {
		switch p.Type {
		case "gemini":
			switch p.AuthMode {
			case "api_key":
				if p.APIKey == "" {
					return fmt.Errorf("provider %q: api_key is required when auth_mode is \"api_key\"", p.Name)
				}
			case "vertex":
				if p.Project == "" {
					return fmt.Errorf("provider %q: project is required when auth_mode is \"vertex\"", p.Name)
				}
				if err := validateCredentialsFile(p); err != nil {
					return err
				}
			default:
				return fmt.Errorf("provider %q: auth_mode must be \"api_key\" or \"vertex\" for gemini providers (got %q)", p.Name, p.AuthMode)
			}
		case "anthropic":
			if p.AuthMode == "vertex" {
				if p.Project == "" {
					return fmt.Errorf("provider %q: project is required when auth_mode is \"vertex\"", p.Name)
				}
				if p.Region == "" {
					return fmt.Errorf("provider %q: region is required when auth_mode is \"vertex\"", p.Name)
				}
				if err := validateCredentialsFile(p); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateCredentialsFile(p ProviderConfig) error {
	if p.CredentialsFile != "" {
		if _, err := os.Stat(p.CredentialsFile); err != nil {
			return fmt.Errorf("provider %q: credentials_file %q: %w", p.Name, p.CredentialsFile, err)
		}
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
func (c *Config) FindModel(modelName string) ([]DispatchTarget, error) {
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
				m := c.ModelList[i]
				return []DispatchTarget{{
					Provider:      m.Provider,
					UpstreamModel: m.UpstreamModel,
					Metadata:      m.Metadata,
				}}, nil
			}
		}
	}

	// Pass 2: wildcard scan (entries ending in "/*").
	for i := range c.ModelList {
		prefix := c.ModelList[i].WildcardPrefix()
		if prefix == "" {
			continue
		}
		if suffix, ok := MatchesWildcard(prefix, modelName); ok {
			return []DispatchTarget{{
				Provider:      c.ModelList[i].Provider,
				UpstreamModel: suffix,
				Metadata:      c.ModelList[i].Metadata,
			}}, nil
		}
	}

	return nil, fmt.Errorf("model not found: %s", modelName)
}

// ResolveModel finds the config entry for a model name. For wildcard-resolved
// models (e.g. "chatgpt/gpt-5.4" matching "chatgpt/*"), it returns the
// wildcard entry as the config entry and the computed upstream model name.
// Returns ok=false if the name doesn't match any config entry.
func (c *Config) ResolveModel(name string) (entry *ModelMapping, upstreamModel string, ok bool) {
	// Exact match.
	for i := range c.ModelList {
		if c.ModelList[i].ModelName == name {
			return &c.ModelList[i], c.ModelList[i].UpstreamModel, true
		}
	}
	// Wildcard match.
	for i := range c.ModelList {
		prefix := c.ModelList[i].WildcardPrefix()
		if prefix == "" {
			continue
		}
		if suffix, matched := MatchesWildcard(prefix, name); matched {
			return &c.ModelList[i], suffix, true
		}
	}
	return nil, "", false
}

// WildcardPrefixes returns a map of wildcard prefix → ModelList index for all
// wildcard entries. Used by the web layer for grouping models under wildcards.
func (c *Config) WildcardPrefixes() map[string]int {
	m := make(map[string]int)
	for i := range c.ModelList {
		if prefix := c.ModelList[i].WildcardPrefix(); prefix != "" {
			m[prefix] = i
		}
	}
	return m
}
