package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/provider"
	"github.com/seckatie/glitchgate/internal/provider/anthropic"
	"github.com/seckatie/glitchgate/internal/provider/copilot"
	"github.com/seckatie/glitchgate/internal/provider/gemini"
	openaiprov "github.com/seckatie/glitchgate/internal/provider/openai"
	"github.com/seckatie/glitchgate/internal/provider/vertex"
)

const defaultOpenAIBaseURL = "https://api.openai.com"
const defaultGeminiBaseURL = gemini.DefaultBaseURL

// ProviderRegistry compiles configured provider clients, pricing tables, and
// legacy provider-name aliases into one runtime dependency.
type ProviderRegistry struct {
	providers                    map[string]provider.Provider
	calculator                   *pricing.Calculator
	providerNames                map[string]string
	providerMonthlySubscriptions map[string]float64
}

// NewProviderRegistry compiles provider clients, default pricing, metadata
// overrides, and provider display aliases from config.
func NewProviderRegistry(cfg *config.Config, requestTimeout time.Duration) (*ProviderRegistry, error) {
	if cfg == nil {
		return nil, fmt.Errorf("provider registry requires config")
	}

	providers := make(map[string]provider.Provider, len(cfg.Providers))
	pricingMap := make(map[string]pricing.Entry)
	providerNames := make(map[string]string, len(cfg.Providers)*2)
	providerMonthlySubscriptions := make(map[string]float64, len(cfg.Providers))
	legacyProviderNames := make(map[string]string, len(cfg.Providers))

	for _, pc := range cfg.Providers {
		client, err := buildProvider(pc, requestTimeout)
		if err != nil {
			return nil, err
		}
		providers[pc.Name] = client

		providerNames[pc.Name] = pc.Name
		if pc.MonthlySubscriptionCost != nil {
			providerMonthlySubscriptions[pc.Name] = *pc.MonthlySubscriptionCost
		}
		addLegacyProviderAlias(providerNames, legacyProviderNames, pc)

		for model, entry := range defaultPricingForProvider(pc) {
			pricingMap[pc.Name+"/"+model] = entry
		}
	}

	for _, mm := range cfg.ModelList {
		if strings.HasSuffix(mm.ModelName, "/*") || len(mm.Fallbacks) > 0 || mm.Metadata == nil {
			continue
		}

		pc, err := cfg.FindProvider(mm.Provider)
		if err != nil {
			continue
		}

		pricingMap[pc.Name+"/"+mm.UpstreamModel] = pricing.Entry{
			InputPerMillion:      mm.Metadata.InputTokenCost,
			OutputPerMillion:     mm.Metadata.OutputTokenCost,
			CacheReadPerMillion:  mm.Metadata.CacheReadCost,
			CacheWritePerMillion: mm.Metadata.CacheWriteCost,
		}
	}

	return &ProviderRegistry{
		providers:                    providers,
		calculator:                   pricing.NewCalculator(pricingMap),
		providerNames:                providerNames,
		providerMonthlySubscriptions: providerMonthlySubscriptions,
	}, nil
}

// Providers returns the configured provider client map keyed by provider name.
func (r *ProviderRegistry) Providers() map[string]provider.Provider {
	if r == nil {
		return nil
	}
	return cloneProviderMap(r.providers)
}

// Calculator returns the compiled pricing calculator.
func (r *ProviderRegistry) Calculator() *pricing.Calculator {
	if r == nil {
		return nil
	}
	return r.calculator
}

// ProviderNames returns display aliases keyed by configured provider name and
// compatible legacy provider identifiers.
func (r *ProviderRegistry) ProviderNames() map[string]string {
	if r == nil {
		return nil
	}
	return cloneStringMap(r.providerNames)
}

// ProviderMonthlySubscriptions returns configured monthly subscription costs
// keyed by provider display name.
func (r *ProviderRegistry) ProviderMonthlySubscriptions() map[string]float64 {
	if r == nil {
		return nil
	}
	return cloneFloatMap(r.providerMonthlySubscriptions)
}

func buildProvider(pc config.ProviderConfig, requestTimeout time.Duration) (provider.Provider, error) {
	switch pc.Type {
	case "anthropic":
		client, err := anthropic.NewClient(pc.Name, pc.BaseURL, pc.AuthMode, pc.APIKey, pc.DefaultVersion)
		if err != nil {
			return nil, fmt.Errorf("anthropic provider %q: %w", pc.Name, err)
		}
		client.SetTimeouts(requestTimeout)
		return client, nil
	case "github_copilot":
		tokenDir := pc.TokenDir
		if tokenDir == "" {
			tokenDir = copilot.DefaultTokenDir()
		}
		client := copilot.NewClient(pc.Name, tokenDir)
		client.SetTimeouts(requestTimeout)
		return client, nil
	case "openai":
		client, err := openaiprov.NewClient(pc.Name, effectiveBaseURL(pc), pc.AuthMode, pc.APIKey, openaiprov.APITypeChatCompletions)
		if err != nil {
			return nil, fmt.Errorf("openai provider %q: %w", pc.Name, err)
		}
		client.SetTimeouts(requestTimeout)
		return client, nil
	case "openai_responses":
		client, err := openaiprov.NewClient(pc.Name, effectiveBaseURL(pc), pc.AuthMode, pc.APIKey, openaiprov.APITypeResponses)
		if err != nil {
			return nil, fmt.Errorf("openai_responses provider %q: %w", pc.Name, err)
		}
		client.SetTimeouts(requestTimeout)
		return client, nil
	case "gemini":
		client, err := gemini.NewClient(pc.Name, effectiveBaseURL(pc), pc.AuthMode, pc.APIKey)
		if err != nil {
			return nil, fmt.Errorf("gemini provider %q: %w", pc.Name, err)
		}
		client.SetTimeouts(requestTimeout)
		return client, nil
	case "vertex_claude":
		client, err := vertex.NewClient(pc.Name, pc.Project, pc.Region, pc.CredentialsFile, pc.DefaultVersion)
		if err != nil {
			return nil, fmt.Errorf("vertex_claude provider %q: %w", pc.Name, err)
		}
		client.SetTimeouts(requestTimeout)
		return client, nil
	case "vertex_gemini":
		client, err := vertex.NewGeminiClient(pc.Name, pc.Project, pc.Region, pc.CredentialsFile)
		if err != nil {
			return nil, fmt.Errorf("vertex_gemini provider %q: %w", pc.Name, err)
		}
		client.SetTimeouts(requestTimeout)
		return client, nil
	default:
		return nil, fmt.Errorf("unsupported provider type %q for provider %q", pc.Type, pc.Name)
	}
}

func defaultPricingForProvider(pc config.ProviderConfig) map[string]pricing.Entry {
	baseURL := effectiveBaseURL(pc)

	switch pc.Type {
	case "github_copilot":
		return pricing.CopilotDefaults
	case "anthropic":
		if pricing.IsOfficialAnthropicURL(baseURL) {
			return pricing.AnthropicDefaults
		}
	case "openai", "openai_responses":
		switch {
		case pricing.IsOfficialOpenAIURL(baseURL):
			return pricing.OpenAIDefaults
		case pricing.IsChutesURL(baseURL):
			return pricing.ChutesDefaults
		case pricing.IsSegmentURL(baseURL):
			return pricing.SegmentDefaults
		}
	case "gemini":
		return pricing.GeminiDefaults
	case "vertex_claude":
		return pricing.AnthropicDefaults
	case "vertex_gemini":
		return pricing.GeminiDefaults
	}

	return nil
}

func addLegacyProviderAlias(providerNames, legacyProviderNames map[string]string, pc config.ProviderConfig) {
	legacyName := pricing.ProviderKey(pc.Type, effectiveBaseURL(pc))
	if legacyName == "" || legacyName == pc.Name {
		return
	}
	if existing, ok := legacyProviderNames[legacyName]; ok && existing != pc.Name {
		delete(providerNames, legacyName)
		return
	}
	legacyProviderNames[legacyName] = pc.Name
	providerNames[legacyName] = pc.Name
}

func effectiveBaseURL(pc config.ProviderConfig) string {
	switch pc.Type {
	case "github_copilot":
		if pc.BaseURL == "" {
			return copilot.DefaultAPIURL
		}
	case "openai", "openai_responses":
		if pc.BaseURL == "" {
			return defaultOpenAIBaseURL
		}
	case "gemini":
		if pc.BaseURL == "" {
			return defaultGeminiBaseURL
		}
	}
	return pc.BaseURL
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneProviderMap(in map[string]provider.Provider) map[string]provider.Provider {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]provider.Provider, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneFloatMap(in map[string]float64) map[string]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]float64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
