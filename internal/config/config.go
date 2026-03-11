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
	Providers    []ProviderConfig `mapstructure:"providers"     yaml:"providers"`
	ModelList    []ModelMapping   `mapstructure:"model_list"    yaml:"model_list"`
	Pricing      []PricingEntry   `mapstructure:"pricing"       yaml:"pricing"`
}

// ProviderConfig describes an upstream LLM provider endpoint.
type ProviderConfig struct {
	Name           string `mapstructure:"name"            yaml:"name"`
	Type           string `mapstructure:"type"            yaml:"type"` // "anthropic" (default)
	BaseURL        string `mapstructure:"base_url"        yaml:"base_url"`
	AuthMode       string `mapstructure:"auth_mode"       yaml:"auth_mode"` // "proxy_key" or "forward"
	APIKey         string `mapstructure:"api_key"         yaml:"api_key"`
	DefaultVersion string `mapstructure:"default_version" yaml:"default_version"`
}

// ModelMapping maps a client-facing model name to an upstream provider and model.
type ModelMapping struct {
	ModelName     string `mapstructure:"model_name"     yaml:"model_name"`
	Provider      string `mapstructure:"provider"       yaml:"provider"`
	UpstreamModel string `mapstructure:"upstream_model" yaml:"upstream_model"`
}

// PricingEntry defines token pricing for an upstream model.
type PricingEntry struct {
	Model            string  `mapstructure:"model"              yaml:"model"`
	InputPerMillion  float64 `mapstructure:"input_per_million"  yaml:"input_per_million"`
	OutputPerMillion float64 `mapstructure:"output_per_million" yaml:"output_per_million"`
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
			v.AddConfigPath(home + "/.config/llm-proxy")
		}
		v.AddConfigPath(".")
		v.AddConfigPath("/etc/llm-proxy")
	}

	// Environment variable support.
	v.SetEnvPrefix("LLM_PROXY")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Defaults.
	v.SetDefault("listen", ":4000")
	v.SetDefault("database_path", "llm-proxy.db")

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

	// Apply provider defaults and expand env vars.
	for i := range cfg.Providers {
		if cfg.Providers[i].Type == "" {
			cfg.Providers[i].Type = "anthropic"
		}
		cfg.Providers[i].APIKey = os.ExpandEnv(cfg.Providers[i].APIKey)
	}

	if cfg.MasterKey == "" {
		return nil, errors.New("master_key is required (set in config file or LLM_PROXY_MASTER_KEY env var)")
	}

	return &cfg, nil
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

// FindModel resolves a client-facing model name to a ModelMapping.
//
// Resolution order:
//  1. Exact match — scan all entries for an identical model_name.
//  2. Wildcard match — scan entries whose model_name ends in "/*".
//     The prefix before "/*" must match the beginning of modelName up to
//     and including the separator "/".  The remainder (suffix) becomes
//     the UpstreamModel.  First wildcard match in config order wins.
//  3. No match — return error.
func (c *Config) FindModel(modelName string) (*ModelMapping, error) {
	// Pass 1: exact match.
	for i := range c.ModelList {
		if c.ModelList[i].ModelName == modelName {
			return &c.ModelList[i], nil
		}
	}

	// Pass 2: wildcard match (entries ending in "/*").
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
			return &ModelMapping{
				ModelName:     modelName,
				Provider:      c.ModelList[i].Provider,
				UpstreamModel: suffix,
			}, nil
		}
	}

	return nil, fmt.Errorf("model not found: %s", modelName)
}
