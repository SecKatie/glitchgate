// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/errgroup"

	"github.com/seckatie/glitchgate/internal/auth"
	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/store"
)

// ModelListItem is a row on the Models list page.
type ModelListItem struct {
	ModelName       string
	ProviderName    string
	ProviderType    string
	ProviderKey     string // raw config provider key (e.g. "anthropic"), used for pricing lookups
	UpstreamModel   string
	IsVirtual       bool
	IsWildcard      bool
	IsUnconfigured  bool // seen in logs but not in model_list config
	IsLogGroup      bool // synthetic group header for all unconfigured models
	Fallbacks       []string
	Pricing         *pricing.Entry
	HasPricing      bool
	EncodedName     string // url.PathEscape(ModelName)
	TotalSpendUSD   float64
	Children        []ModelListItem  // wildcard: matched models from logs; virtual: fallback targets
	ChildCount      int              // number of children (for display)
	RequestCount    int64            // aggregate request count (for wildcards/virtual)
	FallbackDetails []FallbackDetail // resolved fallback chain (virtual models only)
}

// FallbackDetail holds resolved information about a single fallback in a virtual model's chain.
type FallbackDetail struct {
	ModelName     string
	ProviderName  string
	ProviderType  string
	UpstreamModel string
	Pricing       *pricing.Entry
	HasPricing    bool
	EncodedName   string
}

// ModelDetailView is the data passed to the model detail page template.
type ModelDetailView struct {
	ActiveTab         string
	CurrentUser       string
	ModelName         string
	ProviderName      string
	ProviderType      string
	UpstreamModel     string
	IsVirtual         bool
	IsWildcard        bool
	IsUnconfigured    bool
	Fallbacks         []string
	FallbackDetails   []FallbackDetail
	Pricing           *pricing.Entry
	HasPricing        bool
	HasCostData       bool // true when TotalCostUSD is computed (including virtual models)
	Usage             *store.ModelUsageSummary
	CurlExample       string
	MatchedModels     []ModelListItem // wildcard: concrete models seen in logs matching this prefix
	LatencyTimeseries []store.ModelLatencyTimeseriesEntry
	OverallTPS        float64 // overall tokens per second across all data
	MaxTPS            float64 // chart Y-axis ceiling
	MinTPS            float64 // chart Y-axis floor
	ChartPoints       string  // SVG polyline points for the TPS line chart
	ChartFill         string  // SVG polygon points for the area fill
	ChartLabels       []ChartLabel
}

// ChartLabel is a sparse set of X-axis labels for the TPS line chart.
type ChartLabel struct {
	X     float64
	Label string
}

// resolveModelPricing looks up the provider config for providerName and returns the
// provider name, type, and pricing entry for the given upstream model.
func resolveModelPricing(providerMap map[string]config.ProviderConfig, calc *pricing.Calculator, providerName, upstreamModel string) (name, provType string, entry *pricing.Entry, hasPricing bool) {
	pc, ok := providerMap[providerName]
	if !ok {
		return
	}
	name = pc.Name
	provType = pc.Type

	if e, found := calc.Lookup(pc.Name, upstreamModel); found {
		entry = &e
		hasPricing = true
	}
	return
}

// resolveFallbackDetails resolves provider/upstream/pricing for each fallback in a virtual model's chain.
func resolveFallbackDetails(cfg *config.Config, fallbacks []string, providerMap map[string]config.ProviderConfig, calc *pricing.Calculator) []FallbackDetail {
	details := make([]FallbackDetail, len(fallbacks))
	for i, fb := range fallbacks {
		detail := FallbackDetail{
			ModelName:   fb,
			EncodedName: url.PathEscape(fb),
		}
		entry, upstreamModel, ok := cfg.ResolveModel(fb)
		if ok && entry.Provider != "" {
			detail.UpstreamModel = upstreamModel
			detail.ProviderName, detail.ProviderType, detail.Pricing, detail.HasPricing = resolveModelPricing(providerMap, calc, entry.Provider, upstreamModel)
		}
		details[i] = detail
	}
	return details
}

// buildModelList constructs a ModelListItem slice from the model_list config.
// Pure function — no HTTP or template dependencies.
func buildModelList(modelList []config.ModelMapping, providerMap map[string]config.ProviderConfig, calc *pricing.Calculator) []ModelListItem {
	items := make([]ModelListItem, 0, len(modelList))

	for _, m := range modelList {
		item := ModelListItem{
			ModelName:   m.ModelName,
			IsVirtual:   m.IsVirtual(),
			IsWildcard:  m.IsWildcard(),
			Fallbacks:   m.Fallbacks,
			EncodedName: url.PathEscape(m.ModelName),
		}

		if !item.IsVirtual && m.Provider != "" {
			item.ProviderKey = m.Provider
			item.UpstreamModel = m.UpstreamModel
			item.ProviderName, item.ProviderType, item.Pricing, item.HasPricing = resolveModelPricing(providerMap, calc, m.Provider, m.UpstreamModel)
		}

		items = append(items, item)
	}

	return items
}

const modelUsageCacheTTL = 30 * time.Second

// cachedModelUsageSummaries returns the all-model usage map, serving from an
// in-memory TTL cache when possible to avoid redundant full-table aggregations.
func (h *Handlers) cachedModelUsageSummaries(r *http.Request) map[string]*store.ModelUsageSummary {
	h.modelUsageMu.RLock()
	if h.modelUsageCache != nil && time.Since(h.modelUsageCacheTime) < modelUsageCacheTTL {
		cached := h.modelUsageCache
		h.modelUsageMu.RUnlock()
		return cached
	}
	h.modelUsageMu.RUnlock()

	usageMap, err := h.store.GetAllModelUsageSummaries(r.Context())
	if err != nil {
		slog.Error("get all model usage summaries", "error", err)
		return map[string]*store.ModelUsageSummary{}
	}

	h.modelUsageMu.Lock()
	h.modelUsageCache = usageMap
	h.modelUsageCacheTime = time.Now()
	h.modelUsageMu.Unlock()

	return usageMap
}

// ModelsPage renders the model list page.
func (h *Handlers) ModelsPage(w http.ResponseWriter, r *http.Request) {
	items := buildModelList(h.cfg.ModelList, h.providerMap, h.calc)

	// Fetch all model usage, using a TTL cache to avoid re-running
	// the aggregation on rapid/concurrent page loads.
	usageMap := h.cachedModelUsageSummaries(r)

	// Build a map of wildcard prefixes → index in items for grouping.
	wildcardIdx := make(map[string]int) // prefix (without "/*") → items index
	for i, it := range items {
		if it.IsWildcard {
			prefix, _ := strings.CutSuffix(it.ModelName, "/*")
			wildcardIdx[prefix] = i
		}
	}

	// Nest explicitly configured models that match a wildcard under the wildcard group.
	// Track which items to remove from top level.
	nestedAsChild := make(map[int]bool)
	for i, it := range items {
		if it.IsWildcard || it.IsVirtual {
			continue
		}
		for prefix, wcIdx := range wildcardIdx {
			if _, ok := config.MatchesWildcard(prefix, it.ModelName); ok {
				items[wcIdx].Children = append(items[wcIdx].Children, it)
				nestedAsChild[i] = true
				break
			}
		}
	}

	// Remove nested items from top level (iterate backwards to preserve indices).
	if len(nestedAsChild) > 0 {
		filtered := make([]ModelListItem, 0, len(items)-len(nestedAsChild))
		for i, it := range items {
			if !nestedAsChild[i] {
				filtered = append(filtered, it)
			}
		}
		items = filtered
		// Rebuild wildcard index since positions changed.
		wildcardIdx = make(map[string]int, len(wildcardIdx))
		for i, it := range items {
			if it.IsWildcard {
				prefix, _ := strings.CutSuffix(it.ModelName, "/*")
				wildcardIdx[prefix] = i
			}
		}
	}

	// Track configured model names so we can identify log-only models.
	configured := make(map[string]struct{}, len(items))
	for _, it := range items {
		configured[it.ModelName] = struct{}{}
		for _, ch := range it.Children {
			configured[ch.ModelName] = struct{}{}
		}
	}

	// Classify models seen in logs: nest under wildcard or add as top-level unconfigured.
	for name, u := range usageMap {
		if _, ok := configured[name]; ok {
			continue
		}
		child := ModelListItem{
			ModelName:   name,
			EncodedName: url.PathEscape(name),
		}
		if u.ProviderName != "" {
			child.UpstreamModel = u.UpstreamModel
			child.ProviderName, child.ProviderType, child.Pricing, child.HasPricing = resolveModelPricing(h.providerMap, h.calc, u.ProviderName, u.UpstreamModel)
		}
		child.RequestCount = u.RequestCount

		// Check if this model matches a wildcard prefix.
		matched := false
		for prefix, idx := range wildcardIdx {
			if after, ok := config.MatchesWildcard(prefix, name); ok {
				// Resolve pricing from the wildcard's provider.
				if items[idx].ProviderKey != "" {
					child.UpstreamModel = after
					child.ProviderName, child.ProviderType, child.Pricing, child.HasPricing = resolveModelPricing(h.providerMap, h.calc, items[idx].ProviderKey, after)
				}
				items[idx].Children = append(items[idx].Children, child)
				matched = true
				break
			}
		}
		if !matched {
			child.IsUnconfigured = true
			items = append(items, child)
		}
	}

	// Build a reverse index for O(1) lookup by provider+upstream model.
	usageByUpstream := make(map[string]*store.ModelUsageSummary, len(usageMap))
	for _, u := range usageMap {
		if u.ProviderName != "" && u.UpstreamModel != "" {
			usageByUpstream[u.ProviderName+"/"+u.UpstreamModel] = u
		}
	}

	// Populate spend from usage map for all items (top-level and children).
	// lookupUsage finds usage data for a model by name first, then by
	// provider+upstream match (for wildcard-resolved models whose name
	// doesn't appear directly in the usage map).
	lookupUsage := func(item *ModelListItem) *store.ModelUsageSummary {
		if u, ok := usageMap[item.ModelName]; ok {
			return u
		}
		if item.ProviderName != "" && item.UpstreamModel != "" {
			if u, ok := usageByUpstream[item.ProviderName+"/"+item.UpstreamModel]; ok {
				return u
			}
		}
		return nil
	}

	computeSpend := func(item *ModelListItem) {
		u := lookupUsage(item)
		if u == nil {
			return
		}
		item.RequestCount = u.RequestCount
		if item.HasPricing && item.Pricing != nil {
			item.TotalSpendUSD = priceUsage(*item.Pricing, tokenUsage{
				InputTokens:      u.InputTokens,
				CacheWriteTokens: u.CacheCreationInputTokens,
				CacheReadTokens:  u.CacheReadInputTokens,
				OutputTokens:     u.OutputTokens,
			}).TotalCostUSD
		} else if u.LogCostUSD > 0 {
			// Fall back to pre-computed cost from request logs when pricing
			// config is unavailable (e.g. unconfigured models).
			item.TotalSpendUSD = u.LogCostUSD
		}
	}

	for i := range items {
		computeSpend(&items[i])
		for j := range items[i].Children {
			computeSpend(&items[i].Children[j])
		}
	}

	// Resolve virtual model fallback details and create children from them.
	// Also nest fallback targets under matching wildcards so they appear in both places.
	wildcardChildren := make(map[string]map[string]bool) // prefix → set of child names already present
	for prefix, idx := range wildcardIdx {
		m := make(map[string]bool, len(items[idx].Children))
		for _, ch := range items[idx].Children {
			m[ch.ModelName] = true
		}
		wildcardChildren[prefix] = m
		_ = idx // used via wildcardChildren
	}

	for i := range items {
		if !items[i].IsVirtual || len(items[i].Fallbacks) == 0 {
			continue
		}
		items[i].FallbackDetails = resolveFallbackDetails(h.cfg, items[i].Fallbacks, h.providerMap, h.calc)
		items[i].ChildCount = len(items[i].FallbackDetails)
		// Build children from fallback details for uniform template rendering.
		for _, fb := range items[i].FallbackDetails {
			child := ModelListItem{
				ModelName:     fb.ModelName,
				ProviderName:  fb.ProviderName,
				ProviderType:  fb.ProviderType,
				UpstreamModel: fb.UpstreamModel,
				Pricing:       fb.Pricing,
				HasPricing:    fb.HasPricing,
				EncodedName:   fb.EncodedName,
			}
			computeSpend(&child)
			items[i].Children = append(items[i].Children, child)

			// If this fallback target matches a wildcard, ensure it also
			// appears under that wildcard group (models resolved through
			// wildcards at request time may not have their own usage entry).
			for prefix, idx := range wildcardIdx {
				if _, ok := config.MatchesWildcard(prefix, fb.ModelName); ok {
					if !wildcardChildren[prefix][fb.ModelName] {
						wcChild := child // copy
						items[idx].Children = append(items[idx].Children, wcChild)
						wildcardChildren[prefix][fb.ModelName] = true
					}
					break
				}
			}
		}

		// Compute virtual model aggregate spend from pricing groups.
		if u, ok := usageMap[items[i].ModelName]; ok {
			items[i].RequestCount = u.RequestCount
		}
	}

	// Recompute wildcard child counts and aggregate spend after all children are resolved.
	for _, idx := range wildcardIdx {
		items[idx].ChildCount = len(items[idx].Children)
		items[idx].TotalSpendUSD = 0
		items[idx].RequestCount = 0
		for _, ch := range items[idx].Children {
			items[idx].TotalSpendUSD += ch.TotalSpendUSD
			items[idx].RequestCount += ch.RequestCount
		}
	}

	// Group unconfigured (log-only) models into a single collapsible section.
	var logOnly []ModelListItem
	filtered := items[:0]
	for _, it := range items {
		if it.IsUnconfigured {
			logOnly = append(logOnly, it)
		} else {
			filtered = append(filtered, it)
		}
	}
	if len(logOnly) > 0 {
		var groupSpend float64
		var groupReqs int64
		for _, ch := range logOnly {
			groupSpend += ch.TotalSpendUSD
			groupReqs += ch.RequestCount
		}
		filtered = append(filtered, ModelListItem{
			ModelName:     "Seen in logs",
			IsLogGroup:    true,
			IsUnconfigured: true,
			Children:      logOnly,
			ChildCount:    len(logOnly),
			TotalSpendUSD: groupSpend,
			RequestCount:  groupReqs,
			EncodedName:   "_log_group",
		})
		items = filtered
	}

	data := map[string]any{
		"ActiveTab": "models",
		"Models":    items,
	}
	setNavData(data, auth.SessionFromContext(r.Context()))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "models.html", data); err != nil {
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

	// Find the model in the config (exact match or wildcard resolution).
	found, resolvedUpstream, foundOK := h.cfg.ResolveModel(modelName)
	resolvedViaWildcard := foundOK && found.IsWildcard()
	if !foundOK {
		found = nil
	}

	// Run independent DB queries concurrently.
	var (
		usage   *store.ModelUsageSummary
		groups  []store.CostPricingGroup
		latency []store.ModelLatencyTimeseriesEntry
	)

	needGroups := found != nil && len(found.Fallbacks) > 0 // virtual models need pricing groups
	g, ctx := errgroup.WithContext(r.Context())

	g.Go(func() error {
		var err error
		if resolvedViaWildcard && found != nil {
			// Wildcard-resolved models: query by provider + upstream since
			// model_requested in logs is the virtual/client-facing name.
			usage, err = h.store.GetModelUsageSummaryByUpstream(ctx, found.Provider, resolvedUpstream)
		} else {
			usage, err = h.store.GetModelUsageSummary(ctx, modelName)
		}
		return err
	})

	if needGroups {
		g.Go(func() error {
			var err error
			groups, err = h.store.GetModelCostPricingGroups(ctx, modelName)
			return err
		})
	}

	g.Go(func() error {
		var err error
		latency, err = h.store.GetModelLatencyTimeseries(ctx, modelName)
		return err
	})

	if err := g.Wait(); err != nil {
		slog.Error("model detail queries", "model", modelName, "error", err) // #nosec G706 -- slog key-value pairs are structured and safely escaped; no log injection vector
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

	if found != nil && resolvedViaWildcard {
		// Concrete model resolved through a wildcard — treat as a simple concrete model.
		view.UpstreamModel = resolvedUpstream
		if found.Provider != "" {
			view.ProviderName, view.ProviderType, view.Pricing, view.HasPricing = resolveModelPricing(h.providerMap, h.calc, found.Provider, resolvedUpstream)
		}
	} else if found != nil {
		view.UpstreamModel = found.UpstreamModel
		view.IsVirtual = found.IsVirtual()
		view.IsWildcard = found.IsWildcard()
		view.Fallbacks = found.Fallbacks

		// Resolve fallback details for virtual models.
		if view.IsVirtual {
			view.FallbackDetails = resolveFallbackDetails(h.cfg, found.Fallbacks, h.providerMap, h.calc)
		}

		// For wildcard models, find concrete models seen in logs that match the prefix.
		if view.IsWildcard {
			prefix := found.WildcardPrefix()
			allUsage := h.cachedModelUsageSummaries(r)
			for name, u := range allUsage {
				if after, ok := config.MatchesWildcard(prefix, name); ok {
					child := ModelListItem{
						ModelName:    name,
						EncodedName:  url.PathEscape(name),
						RequestCount: u.RequestCount,
					}
					if found.Provider != "" {
						child.UpstreamModel = after
						child.ProviderName, child.ProviderType, child.Pricing, child.HasPricing = resolveModelPricing(h.providerMap, h.calc, found.Provider, after)
					}
					if child.HasPricing && child.Pricing != nil {
						child.TotalSpendUSD = priceUsage(*child.Pricing, tokenUsage{
							InputTokens:      u.InputTokens,
							CacheWriteTokens: u.CacheCreationInputTokens,
							CacheReadTokens:  u.CacheReadInputTokens,
							OutputTokens:     u.OutputTokens,
						}).TotalCostUSD
					}
					view.MatchedModels = append(view.MatchedModels, child)
				}
			}
		}
	}

	if found != nil && !resolvedViaWildcard && !view.IsVirtual && found.Provider != "" {
		view.ProviderName, view.ProviderType, view.Pricing, view.HasPricing = resolveModelPricing(h.providerMap, h.calc, found.Provider, found.UpstreamModel)
	} else if found == nil && usage.ProviderName != "" {
		view.ProviderName, view.ProviderType, view.Pricing, view.HasPricing = resolveModelPricing(h.providerMap, h.calc, usage.ProviderName, usage.UpstreamModel)
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
	} else if view.IsVirtual && len(groups) > 0 {
		// Virtual models route across multiple upstream models — compute cost per pricing group.
		agg := computeAggregateCostBreakdownWithAliases(groups, h.calc, h.providerNames)
		if agg.HasAnyPricing {
			usage.TotalCostUSD = agg.TotalCostUSD
			view.HasCostData = true
		}
	}

	// Fall back to pre-computed cost from request logs when pricing config is unavailable.
	if !view.HasCostData && usage.LogCostUSD > 0 {
		usage.TotalCostUSD = usage.LogCostUSD
		view.HasCostData = true
	}

	// Build the performance chart from latency data.
	if len(latency) > 0 {
		view.LatencyTimeseries = latency

		// Compute per-bucket TPS and track min/max for the chart Y-axis.
		tpsValues := make([]float64, len(latency))
		view.MinTPS = math.MaxFloat64
		var totalMs, totalTok int64
		for i, e := range latency {
			totalMs += e.TotalLatencyMs
			totalTok += e.TotalOutputTokens
			if e.AvgMsPerOutputToken > 0 {
				tps := 1000.0 / e.AvgMsPerOutputToken
				tpsValues[i] = tps
				if tps > view.MaxTPS {
					view.MaxTPS = tps
				}
				if tps < view.MinTPS {
					view.MinTPS = tps
				}
			}
		}
		if totalTok > 0 && totalMs > 0 {
			view.OverallTPS = float64(totalTok) / (float64(totalMs) / 1000.0)
		}
		if len(latency) < 2 {
			view.MinTPS = 0
		}
		view.ChartPoints, view.ChartFill, view.ChartLabels = buildTPSChartSVG(latency, tpsValues, view.MinTPS, view.MaxTPS)
	}

	if sc := auth.SessionFromContext(r.Context()); sc != nil {
		if sc.IsMasterKey {
			view.CurrentUser = "admin"
		} else if sc.User != nil {
			view.CurrentUser = sc.User.DisplayName
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

// buildTPSChartSVG generates SVG polyline/polygon point strings and sparse
// X-axis labels for the TPS line chart. The SVG viewBox is 600x200.
func buildTPSChartSVG(entries []store.ModelLatencyTimeseriesEntry, tpsValues []float64, yMin, yMax float64) (points, fill string, labels []ChartLabel) {
	n := len(entries)
	if n < 2 {
		return "", "", nil
	}

	const (
		w = 600.0
		h = 200.0
	)

	// Add 10% padding above max so the line doesn't touch the top.
	yRange := yMax - yMin
	if yRange <= 0 {
		yRange = 1
	}
	paddedMax := yMax + yRange*0.1

	var pts strings.Builder
	for i, tps := range tpsValues {
		x := (float64(i) / float64(n-1)) * w
		y := h - ((tps-yMin)/(paddedMax-yMin))*h
		if i > 0 {
			pts.WriteByte(' ')
		}
		fmt.Fprintf(&pts, "%.1f,%.1f", x, y)
	}
	points = pts.String()

	// Polygon fill: same points plus bottom-right and bottom-left corners.
	fill = fmt.Sprintf("%s %.1f,%.1f %.1f,%.1f", points, w, h, 0.0, h)

	// Sparse labels: show ~8 evenly spaced labels so they don't overlap.
	maxLabels := min(8, n)
	step := float64(n-1) / float64(maxLabels)
	for i := range maxLabels {
		idx := int(math.Round(float64(i) * step))
		if idx >= n {
			idx = n - 1
		}
		x := (float64(idx) / float64(n-1)) * w
		bucket := entries[idx].Bucket
		// Format: "MM-DD HH:00" from "YYYY-MM-DDTHH" or "YYYY-MM-DD HH"
		lbl := bucket
		if len(bucket) >= 13 {
			lbl = bucket[5:10] + " " + bucket[11:13] + ":00"
		}
		labels = append(labels, ChartLabel{X: x, Label: lbl})
	}

	return points, fill, labels
}
