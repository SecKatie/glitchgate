// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/go-chi/chi/v5"

	"github.com/seckatie/glitchgate/internal/auth"
	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/store"
)

// ProviderHandlers groups the HTTP handlers for the providers page.
type ProviderHandlers struct {
	costStore                    store.CostQueryStore
	modelUsageStore              store.ModelUsageStore
	templates                    *TemplateSet
	tz                           *time.Location
	calc                         *pricing.Calculator
	providerNames                map[string]string
	providerMonthlySubscriptions map[string]float64
	cfg                          *config.Config
	providerMap                  map[string]config.ProviderConfig
}

// NewProviderHandlers creates a new ProviderHandlers.
func NewProviderHandlers(
	cs store.CostQueryStore,
	mus store.ModelUsageStore,
	tmpl *TemplateSet,
	tz *time.Location,
	calc *pricing.Calculator,
	providerNames map[string]string,
	providerMonthlySubscriptions map[string]float64,
	cfg *config.Config,
) *ProviderHandlers {
	if tz == nil {
		tz = time.UTC
	}
	pm := make(map[string]config.ProviderConfig, len(cfg.Providers))
	for _, pc := range cfg.Providers {
		pm[pc.Name] = pc
	}
	return &ProviderHandlers{
		costStore:                    cs,
		modelUsageStore:              mus,
		templates:                    tmpl,
		tz:                           tz,
		calc:                         calc,
		providerNames:                providerNames,
		providerMonthlySubscriptions: providerMonthlySubscriptions,
		cfg:                          cfg,
		providerMap:                  pm,
	}
}

// ProviderRow is a row on the providers list page.
type ProviderRow struct {
	Name                   string
	DisplayName            string
	Type                   string
	Requests               int64
	TokenCostUSD           float64
	MonthlySubscriptionUSD float64
	HasSubscription        bool
	SavingsPct             *float64
	EncodedName            string
}

// ProvidersPageHandler renders the providers list page with subsidy analysis.
func (h *ProviderHandlers) ProvidersPageHandler(w http.ResponseWriter, r *http.Request) {
	costParams := costParamsLast30Days(h.tz, "provider")
	applyScopeToCostParams(auth.SessionFromContext(r.Context()), &costParams)

	mtdCostParams := costParamsMonthToDate(h.tz, "provider")
	applyScopeToCostParams(auth.SessionFromContext(r.Context()), &mtdCostParams)

	var (
		pricingGroups           []store.CostPricingGroup
		timeseriesPricingGroups []store.CostTimeseriesPricingGroup
		mtdPricingGroups        []store.CostPricingGroup
	)

	g, ctx := errgroup.WithContext(r.Context())
	g.Go(func() error {
		var err error
		pricingGroups, err = h.costStore.GetCostPricingGroups(ctx, costParams)
		return err
	})
	g.Go(func() error {
		var err error
		timeseriesPricingGroups, err = h.costStore.GetCostTimeseriesPricingGroups(ctx, costParams)
		return err
	})
	g.Go(func() error {
		var err error
		mtdPricingGroups, err = h.costStore.GetCostPricingGroups(ctx, mtdCostParams)
		return err
	})
	if err := g.Wait(); err != nil {
		slog.Error("providers page: data fetch failed", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	breakdown := deriveBreakdownFromPricingGroups(pricingGroups, "provider", h.providerNames)
	breakdownCosts := buildBreakdownCosts(pricingGroups, h.calc, "provider", h.providerNames)
	providerComparisons, providerComparisonSummary := buildProviderSpendComparisons(breakdown, breakdownCosts, h.providerMonthlySubscriptions)

	subsidyAnalysis := buildSubsidyAnalysis(
		pricingGroups, timeseriesPricingGroups,
		h.calc, h.providerNames, h.providerMonthlySubscriptions, 30,
	)

	mtdCosts := computeAggregateCostBreakdownWithAliases(mtdPricingGroups, h.calc, h.providerNames)
	var subscriptionCostUSD float64
	if subsidyAnalysis != nil {
		subscriptionCostUSD = subsidyAnalysis.SubscriptionCostUSD
	}
	monthlyProjection := buildMonthlyProjection(mtdCosts.TotalCostUSD, h.tz, subscriptionCostUSD)

	// Build provider rows from breakdown.
	var rows []ProviderRow
	for _, e := range breakdown {
		displayName := e.Group
		if mapped, ok := h.providerNames[e.Group]; ok && mapped != "" {
			displayName = mapped
		}

		row := ProviderRow{
			Name:         e.Group,
			DisplayName:  displayName,
			Requests:     e.Requests,
			TokenCostUSD: breakdownCosts[e.Group],
			EncodedName:  url.PathEscape(e.Group),
		}

		// Find provider type.
		if pc, ok := h.providerMap[e.Group]; ok {
			row.Type = pc.Type
		}

		if pc, ok := providerComparisons[e.Group]; ok {
			row.MonthlySubscriptionUSD = pc.MonthlySubscriptionCost
			row.HasSubscription = pc.MonthlySubscriptionCost > 0
			row.SavingsPct = pc.TokenVsSubscriptionPct
		}

		rows = append(rows, row)
	}

	// Also add configured providers that have no usage yet.
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		seen[row.Name] = true
	}
	for name, pc := range h.providerMap {
		if seen[name] {
			continue
		}
		displayName := name
		if mapped, ok := h.providerNames[name]; ok && mapped != "" {
			displayName = mapped
		}
		row := ProviderRow{
			Name:        name,
			DisplayName: displayName,
			Type:        pc.Type,
			EncodedName: url.PathEscape(name),
		}
		if sub, ok := h.providerMonthlySubscriptions[name]; ok {
			row.MonthlySubscriptionUSD = sub
			row.HasSubscription = true
		}
		rows = append(rows, row)
	}

	data := map[string]any{
		"ActiveTab":                 "providers",
		"Title":                     "Providers",
		"Providers":                 rows,
		"SubsidyAnalysis":           subsidyAnalysis,
		"MonthlyProjection":         monthlyProjection,
		"ProviderComparisons":       providerComparisons,
		"ProviderComparisonSummary": providerComparisonSummary,
		"ProviderNames":             h.providerNames,
	}

	setNavData(data, auth.SessionFromContext(r.Context()))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "providers.html", data); err != nil {
		http.Error(w, fmt.Sprintf("Template error: %v", err), http.StatusInternalServerError)
	}
}

// ProviderDetailPageHandler renders the detail page for a single provider.
func (h *ProviderHandlers) ProviderDetailPageHandler(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		http.NotFound(w, r)
		return
	}

	// Unescape in case it was URL-encoded.
	if unescaped, err := url.PathUnescape(name); err == nil {
		name = unescaped
	}

	pc, ok := h.providerMap[name]
	if !ok {
		http.NotFound(w, r)
		return
	}

	displayName := name
	if mapped, ok := h.providerNames[name]; ok && mapped != "" {
		displayName = mapped
	}

	var subscriptionCost float64
	hasSubscription := false
	if sub, ok := h.providerMonthlySubscriptions[name]; ok {
		subscriptionCost = sub
		hasSubscription = true
	}

	// Build model list filtered to this provider.
	allModels := buildModelList(h.cfg.ModelList, h.providerMap, h.calc)

	// Fetch usage summaries for spend data.
	usageMap, err := h.modelUsageStore.GetAllModelUsageSummaries(r.Context())
	if err != nil {
		slog.Error("provider detail: get model usage", "error", err)
		usageMap = map[string]*store.ModelUsageSummary{}
	}

	type modelRow struct {
		ModelName     string
		UpstreamModel string
		InputPerMTok  *float64
		OutputPerMTok *float64
		TotalSpendUSD float64
		EncodedName   string
	}

	var models []modelRow
	var totalSpend float64

	for _, m := range allModels {
		if m.ProviderName != pc.Name || m.IsVirtual {
			continue
		}

		row := modelRow{
			ModelName:     m.ModelName,
			UpstreamModel: m.UpstreamModel,
			EncodedName:   m.EncodedName,
		}

		if m.HasPricing && m.Pricing != nil {
			inputRate := m.Pricing.InputPerMillion
			outputRate := m.Pricing.OutputPerMillion
			row.InputPerMTok = &inputRate
			row.OutputPerMTok = &outputRate
		}

		if u, ok := usageMap[m.ModelName]; ok {
			if m.HasPricing && m.Pricing != nil {
				row.TotalSpendUSD = priceUsage(*m.Pricing, tokenUsage{
					InputTokens:      u.InputTokens,
					CacheWriteTokens: u.CacheCreationInputTokens,
					CacheReadTokens:  u.CacheReadInputTokens,
					OutputTokens:     u.OutputTokens,
				}).TotalCostUSD
			} else if u.LogCostUSD > 0 {
				row.TotalSpendUSD = u.LogCostUSD
			}
		}

		totalSpend += row.TotalSpendUSD
		models = append(models, row)
	}

	data := map[string]any{
		"ActiveTab":        "providers",
		"Title":            displayName + " — Provider",
		"ProviderName":     displayName,
		"ProviderType":     pc.Type,
		"SubscriptionCost": subscriptionCost,
		"HasSubscription":  hasSubscription,
		"Models":           models,
		"TotalSpend":       totalSpend,
	}

	setNavData(data, auth.SessionFromContext(r.Context()))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "provider_detail.html", data); err != nil {
		http.Error(w, fmt.Sprintf("Template error: %v", err), http.StatusInternalServerError)
	}
}
