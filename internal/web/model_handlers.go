// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"codeberg.org/kglitchy/glitchgate/internal/config"
	"codeberg.org/kglitchy/glitchgate/internal/pricing"
	"codeberg.org/kglitchy/glitchgate/internal/store"
)

// ModelListItem is a row on the Models list page.
type ModelListItem struct {
	ModelName      string
	ProviderName   string
	ProviderType   string
	IsVirtual      bool
	IsWildcard     bool
	IsUnconfigured bool // seen in logs but not in model_list config
	Fallbacks      []string
	Pricing        *pricing.Entry
	HasPricing     bool
	EncodedName    string // url.PathEscape(ModelName)
	TotalSpendUSD  float64
}

// ModelDetailView is the data passed to the model detail page template.
type ModelDetailView struct {
	ActiveTab      string
	CurrentUser    string
	ModelName      string
	ProviderName   string
	ProviderType   string
	UpstreamModel  string
	IsVirtual      bool
	IsWildcard     bool
	IsUnconfigured bool
	Fallbacks      []string
	Pricing        *pricing.Entry
	HasPricing     bool
	HasCostData    bool // true when TotalCostUSD is computed (including virtual models)
	Usage          *store.ModelUsageSummary
	CurlExample    string
}

// resolveModelPricing looks up the provider config for providerName and returns the
// provider name, type, and pricing entry for the given upstream model.
func resolveModelPricing(providers []config.ProviderConfig, calc *pricing.Calculator, providerName, upstreamModel string) (name, provType string, entry *pricing.Entry, hasPricing bool) {
	for _, pc := range providers {
		if pc.Name != providerName {
			continue
		}
		name = pc.Name
		provType = pc.Type

		if e, ok := calc.Lookup(pc.Name, upstreamModel); ok {
			entry = &e
			hasPricing = true
		}
		return
	}
	return
}

// buildModelList constructs a ModelListItem slice from the model_list config.
// Pure function — no HTTP or template dependencies.
func buildModelList(modelList []config.ModelMapping, providers []config.ProviderConfig, calc *pricing.Calculator) []ModelListItem {
	items := make([]ModelListItem, 0, len(modelList))

	for _, m := range modelList {
		item := ModelListItem{
			ModelName:   m.ModelName,
			IsVirtual:   len(m.Fallbacks) > 0,
			IsWildcard:  strings.HasSuffix(m.ModelName, "/*"),
			Fallbacks:   m.Fallbacks,
			EncodedName: url.PathEscape(m.ModelName),
		}

		if !item.IsVirtual && m.Provider != "" {
			item.ProviderName, item.ProviderType, item.Pricing, item.HasPricing = resolveModelPricing(providers, calc, m.Provider, m.UpstreamModel)
		}

		items = append(items, item)
	}

	return items
}

// ModelsPage renders the model list page.
func (h *Handlers) ModelsPage(w http.ResponseWriter, r *http.Request) {
	items := buildModelList(h.modelList, h.providers, h.calc)

	// Fetch all model usage in a single query.
	usageMap, err := h.store.GetAllModelUsageSummaries(r.Context())
	if err != nil {
		slog.Error("get all model usage summaries", "error", err)
		usageMap = map[string]*store.ModelUsageSummary{}
	}

	// Track configured model names so we can identify log-only models.
	configured := make(map[string]struct{}, len(items))
	for _, it := range items {
		configured[it.ModelName] = struct{}{}
	}

	// Append models seen in logs that are not in the config.
	for name, u := range usageMap {
		if _, ok := configured[name]; !ok {
			item := ModelListItem{
				ModelName:      name,
				IsUnconfigured: true,
				EncodedName:    url.PathEscape(name),
			}
			if u.ProviderName != "" {
				item.ProviderName, item.ProviderType, item.Pricing, item.HasPricing = resolveModelPricing(h.providers, h.calc, u.ProviderName, u.UpstreamModel)
			}
			items = append(items, item)
		}
	}

	// Populate spend from the usage map, computing cost from pricing when available.
	for i := range items {
		u, ok := usageMap[items[i].ModelName]
		if !ok {
			continue
		}
		if items[i].HasPricing && items[i].Pricing != nil {
			items[i].TotalSpendUSD = priceUsage(*items[i].Pricing, tokenUsage{
				InputTokens:      u.InputTokens,
				CacheWriteTokens: u.CacheCreationInputTokens,
				CacheReadTokens:  u.CacheReadInputTokens,
				OutputTokens:     u.OutputTokens,
			}).TotalCostUSD
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "models.html", map[string]any{
		"ActiveTab": "models",
		"Models":    items,
	}); err != nil {
		slog.Error("render models page", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// ModelDetailPage renders the detail page for a single model.
func (h *Handlers) ModelDetailPage(w http.ResponseWriter, r *http.Request) {
	encoded := chi.URLParam(r, "*")
	modelName, err := url.PathUnescape(encoded)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Find the model in the config list.
	var found *config.ModelMapping
	for i := range h.modelList {
		if h.modelList[i].ModelName == modelName {
			found = &h.modelList[i]
			break
		}
	}

	// Fetch usage stats — needed both for configured and log-only models.
	usage, err := h.store.GetModelUsageSummary(r.Context(), modelName)
	if err != nil {
		slog.Error("get model usage summary", "model", modelName, "error", err) // #nosec G706 -- slog key-value pairs are structured and safely escaped; no log injection vector
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// If not in config and never seen in logs, it truly doesn't exist.
	if found == nil && usage.RequestCount == 0 {
		http.NotFound(w, r)
		return
	}

	view := ModelDetailView{
		ActiveTab:      "models",
		ModelName:      modelName,
		IsUnconfigured: found == nil,
		Usage:          usage,
		CurlExample:    buildCurlExample(modelName),
	}

	if found != nil {
		view.UpstreamModel = found.UpstreamModel
		view.IsVirtual = len(found.Fallbacks) > 0
		view.IsWildcard = strings.HasSuffix(found.ModelName, "/*")
		view.Fallbacks = found.Fallbacks
	}

	if found != nil && !view.IsVirtual && found.Provider != "" {
		view.ProviderName, view.ProviderType, view.Pricing, view.HasPricing = resolveModelPricing(h.providers, h.calc, found.Provider, found.UpstreamModel)
	} else if found == nil && usage.ProviderName != "" {
		view.ProviderName, view.ProviderType, view.Pricing, view.HasPricing = resolveModelPricing(h.providers, h.calc, usage.ProviderName, usage.UpstreamModel)
		view.UpstreamModel = usage.UpstreamModel
	}

	if view.HasPricing && view.Pricing != nil {
		usage.TotalCostUSD = priceUsage(*view.Pricing, tokenUsage{
			InputTokens:      usage.InputTokens,
			CacheWriteTokens: usage.CacheCreationInputTokens,
			CacheReadTokens:  usage.CacheReadInputTokens,
			OutputTokens:     usage.OutputTokens,
		}).TotalCostUSD
		view.HasCostData = true
	} else if view.IsVirtual {
		// Virtual models route across multiple upstream models — compute cost per pricing group.
		groups, err := h.store.GetModelCostPricingGroups(r.Context(), modelName)
		if err != nil {
			slog.Error("get model cost pricing groups", "model", modelName, "error", err) // #nosec G706 -- slog key-value pairs are structured and safely escaped; no log injection vector
		} else {
			agg := computeAggregateCostBreakdown(groups, h.calc)
			if agg.HasAnyPricing {
				usage.TotalCostUSD = agg.TotalCostUSD
				view.HasCostData = true
			}
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "model_detail.html", view); err != nil {
		slog.Error("render model detail page", "error", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// buildCurlExample returns a pre-formatted curl command for the given model name.
func buildCurlExample(modelName string) string {
	return fmt.Sprintf(`curl https://your-glitchgate-host/v1/messages \
  -H "x-api-key: YOUR_PROXY_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "%s",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello!"}]
  }'`, modelName)
}
