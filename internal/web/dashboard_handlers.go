// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/seckatie/glitchgate/internal/auth"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/store"
	"github.com/seckatie/glitchgate/internal/web/billing"
)

// DashboardHandlers groups the HTTP handlers for the overview dashboard.
type DashboardHandlers struct {
	costStore                    store.CostQueryStore
	budgetStore                  store.BudgetCheckStore
	keyStore                     store.ProxyKeyStore
	logStore                     store.RequestLogStore
	templates                    *TemplateSet
	tz                           *time.Location
	calc                         *pricing.Calculator
	providerNames                map[string]string
	providerMonthlySubscriptions map[string]float64
}

// NewDashboardHandlers creates a new DashboardHandlers.
func NewDashboardHandlers(
	cs store.CostQueryStore,
	bs store.BudgetCheckStore,
	ks store.ProxyKeyStore,
	ls store.RequestLogStore,
	tmpl *TemplateSet,
	tz *time.Location,
	calc *pricing.Calculator,
	providerNames map[string]string,
	providerMonthlySubscriptions map[string]float64,
) *DashboardHandlers {
	if tz == nil {
		tz = time.UTC
	}
	return &DashboardHandlers{
		costStore:                    cs,
		budgetStore:                  bs,
		keyStore:                     ks,
		logStore:                     ls,
		templates:                    tmpl,
		tz:                           tz,
		calc:                         calc,
		providerNames:                providerNames,
		providerMonthlySubscriptions: providerMonthlySubscriptions,
	}
}

// DashboardPageHandler renders the overview dashboard.
func (h *DashboardHandlers) DashboardPageHandler(w http.ResponseWriter, r *http.Request) {
	now := time.Now().In(h.tz)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, h.tz)

	costParams := costParamsLast30Days(h.tz, "model")
	applyScopeToCostParams(auth.SessionFromContext(r.Context()), &costParams)

	// Daily cost params for top models (today only).
	todayCostParams := costParamsToday(h.tz, "model")
	applyScopeToCostParams(auth.SessionFromContext(r.Context()), &todayCostParams)

	// Month-to-date params for the monthly spend projection.
	mtdCostParams := costParamsMonthToDate(h.tz, "model")
	applyScopeToCostParams(auth.SessionFromContext(r.Context()), &mtdCostParams)

	var (
		pricingGroups           []store.CostPricingGroup
		timeseriesPricingGroups []store.CostTimeseriesPricingGroup
		todayPricingGroups      []store.CostPricingGroup
		mtdPricingGroups        []store.CostPricingGroup
		activityStats           *store.ActivityStats
		budgets                 []store.ApplicableBudget
	)

	sc := auth.SessionFromContext(r.Context())

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
		todayPricingGroups, err = h.costStore.GetCostPricingGroups(ctx, todayCostParams)
		return err
	})
	g.Go(func() error {
		var err error
		mtdPricingGroups, err = h.costStore.GetCostPricingGroups(ctx, mtdCostParams)
		return err
	})
	g.Go(func() error {
		var err error
		activityStats, err = h.logStore.GetActivityStats(ctx, todayStart)
		return err
	})
	g.Go(func() error {
		scopeType, scopeUserID, scopeTeamID := buildScopeParams(sc)
		var err error
		budgets, err = h.budgetStore.GetBudgetsForScope(ctx, scopeType, scopeUserID, scopeTeamID)
		return err
	})

	if err := g.Wait(); err != nil {
		slog.Error("dashboard: data fetch failed", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Cost summary from pricing groups.
	summary := billing.DeriveSummaryFromPricingGroups(pricingGroups)
	tokenCosts := billing.ComputeAggregateCostBreakdownWithAliases(pricingGroups, h.calc, h.providerNames)

	// Daily trend.
	timeseries := aggregatePricedTimeseries(
		priceTimeseriesGroups(timeseriesPricingGroups, h.calc, h.providerNames), "day",
	)
	var maxCost float64
	for _, e := range timeseries {
		if e.CostUSD > maxCost {
			maxCost = e.CostUSD
		}
	}

	// Top 5 models by cost (today only).
	breakdown := billing.DeriveBreakdownFromPricingGroups(todayPricingGroups, "model", h.providerNames)
	breakdownCosts := billing.BuildBreakdownCosts(todayPricingGroups, h.calc, "model", h.providerNames)
	type topModel struct {
		Name    string
		CostUSD float64
	}
	var topModels []topModel
	for _, e := range breakdown {
		cost := breakdownCosts[e.Group]
		topModels = append(topModels, topModel{Name: e.Group, CostUSD: cost})
	}
	slices.SortFunc(topModels, func(a, b topModel) int {
		if a.CostUSD > b.CostUSD {
			return -1
		}
		if a.CostUSD < b.CostUSD {
			return 1
		}
		return 0
	})
	if len(topModels) > 5 {
		topModels = topModels[:5]
	}

	// Provider subsidy.
	subsidyAnalysis := billing.BuildSubsidyAnalysis(
		pricingGroups, timeseriesPricingGroups,
		h.calc, h.providerNames, h.providerMonthlySubscriptions, 30,
	)

	// Monthly spend projection from MTD data.
	mtdCosts := billing.ComputeAggregateCostBreakdownWithAliases(mtdPricingGroups, h.calc, h.providerNames)
	var subscriptionCostUSD float64
	if subsidyAnalysis != nil {
		subscriptionCostUSD = subsidyAnalysis.SubscriptionCostUSD
	}
	monthlyProjection := billing.BuildMonthlyProjection(mtdCosts.TotalCostUSD, h.tz, subscriptionCostUSD)

	// Budget status.
	budgetEntries := BuildBudgetStatusEntries(r.Context(), budgets, h.budgetStore, h.keyStore, h.tz)
	isGA := sc != nil && (sc.IsMasterKey || sc.Role == "global_admin")
	isAdmin := sc != nil && (sc.IsMasterKey || sc.Role == "global_admin" || sc.Role == "team_admin")

	// Activity stats fallback.
	if activityStats == nil {
		activityStats = &store.ActivityStats{}
	}
	var errorPct float64
	if activityStats.TotalRequests > 0 {
		errorPct = float64(activityStats.ErrorCount) / float64(activityStats.TotalRequests) * 100
	}
	var avgLatencySec float64
	if activityStats.TotalRequests > 0 {
		avgLatencySec = activityStats.AvgLatencyMs / 1000.0
	}

	totalTokens := summary.TotalInputTokens + summary.TotalCacheCreationTokens + summary.TotalCacheReadTokens + summary.TotalOutputTokens

	data := map[string]any{
		"ActiveTab":         "dashboard",
		"Title":             "Dashboard",
		"TokenCosts":        tokenCosts,
		"Summary":           summary,
		"TotalTokens":       totalTokens,
		"Timeseries":        timeseries,
		"MaxCost":           maxCost,
		"TopModels":         topModels,
		"SubsidyAnalysis":   subsidyAnalysis,
		"MonthlyProjection": monthlyProjection,
		"BudgetEntries":     budgetEntries,
		"IsGA":              isGA,
		"IsAdmin":           isAdmin,
		"ActivityStats":     activityStats,
		"ErrorPct":          errorPct,
		"AvgLatencySec":     avgLatencySec,
	}

	setNavData(data, sc)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "dashboard.html", data); err != nil {
		http.Error(w, fmt.Sprintf("Template error: %v", err), http.StatusInternalServerError)
	}
}
