// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/seckatie/glitchgate/internal/provider"
)

// discoveryCapableTypes lists provider types that support model discovery.
var discoveryCapableTypes = map[string]bool{
	"anthropic":        true,
	"openai":           true,
	"openai_responses": true,
	"gemini":           true,
}

// validateDiscoveryProviders rejects discover_models: true on unsupported provider types.
func validateDiscoveryProviders(providers []ProviderConfig) error {
	for _, p := range providers {
		if p.DiscoverModels && !discoveryCapableTypes[p.Type] {
			return fmt.Errorf("provider %q (type %q): discover_models is not supported for this provider type", p.Name, p.Type)
		}
	}
	return nil
}

// matchDiscoverFilter checks whether a model ID passes the discover_filter patterns.
// Returns true if the model should be included, false if excluded.
//
// Semantics:
//   - Empty filters → include all models
//   - Patterns without "!" prefix are include patterns
//   - Patterns with "!" prefix are exclude patterns
//   - If only exclude patterns exist, models not excluded are included
//   - If any include patterns exist, a model must match at least one include pattern
//   - Exclude patterns take precedence over include patterns
func matchDiscoverFilter(modelID string, filters []string) (bool, error) {
	if len(filters) == 0 {
		return true, nil
	}

	var hasInclude bool
	var included bool

	for _, pattern := range filters {
		if strings.HasPrefix(pattern, "!") {
			// Exclude pattern — check if model matches.
			exclude := pattern[1:]
			matched, err := filepath.Match(exclude, modelID)
			if err != nil {
				return false, fmt.Errorf("invalid discover_filter pattern %q: %w", pattern, err)
			}
			if matched {
				return false, nil // Exclude takes precedence.
			}
		} else {
			// Include pattern.
			hasInclude = true
			matched, err := filepath.Match(pattern, modelID)
			if err != nil {
				return false, fmt.Errorf("invalid discover_filter pattern %q: %w", pattern, err)
			}
			if matched {
				included = true
			}
		}
	}

	// If no include patterns were specified, include by default (only excludes active).
	if !hasInclude {
		return true, nil
	}
	return included, nil
}

// InjectDiscoveredModels queries providers that implement ModelDiscoverer,
// applies discover_filter, and appends synthetic ModelMapping entries to
// ModelList before rebuilding resolvedChains.
func (c *Config) InjectDiscoveredModels(providers map[string]provider.Provider) error {
	// Build a set of existing explicit model names for precedence checking.
	explicit := make(map[string]bool, len(c.ModelList))
	for _, m := range c.ModelList {
		explicit[m.ModelName] = true
	}

	for i := range c.Providers {
		pc := &c.Providers[i]
		if !pc.DiscoverModels {
			continue
		}

		p, ok := providers[pc.Name]
		if !ok {
			slog.Warn("discovery: provider not found in registry, skipping",
				"provider", pc.Name)
			continue
		}

		discoverer, ok := p.(provider.ModelDiscoverer)
		if !ok {
			slog.Warn("discovery: provider does not implement ModelDiscoverer, skipping",
				"provider", pc.Name)
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		models, err := discoverer.ListModels(ctx)
		cancel()

		if err != nil {
			slog.Warn("discovery: failed to list models, continuing without discovered models",
				"provider", pc.Name,
				"error", err)
			continue
		}

		prefix := pc.Name + "/"
		if pc.ModelPrefix != nil {
			prefix = *pc.ModelPrefix
		}

		var added int
		for _, m := range models {
			ok, err := matchDiscoverFilter(m.ID, pc.DiscoverFilter)
			if err != nil {
				return fmt.Errorf("provider %q: %w", pc.Name, err)
			}
			if !ok {
				continue
			}

			modelName := prefix + m.ID
			if explicit[modelName] {
				slog.Debug("discovery: explicit entry takes precedence, skipping discovered model",
					"provider", pc.Name,
					"model", modelName)
				continue
			}

			c.ModelList = append(c.ModelList, ModelMapping{
				ModelName:     modelName,
				Provider:      pc.Name,
				UpstreamModel: m.ID,
			})
			explicit[modelName] = true
			added++
		}

		slog.Info("discovery: discovered models",
			"provider", pc.Name,
			"total", len(models),
			"added", added)
	}

	// Rebuild resolved chains with the new model list entries.
	return c.buildResolvedChains()
}
