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

	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/store"
)

// ModelListItem is a row on the Models list page.
type ModelListItem struct {
	ModelName      string
	ProviderName   string
	ProviderType   string
	UpstreamModel  string
	IsVirtual      bool
	IsWildcard     bool
	IsUnconfigured bool // seen in logs but not in model_list config
	Fallbacks      []string
	Pricing        *pricing.Entry
	HasPricing     bool
	EncodedName    string // url.PathEscape(ModelName)
	TotalSpendUSD  float64
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

// buildModelList constructs a ModelListItem slice from the model_list config.
// Pure function — no HTTP or template dependencies.
func buildModelList(modelList []config.ModelMapping, providerMap map[string]config.ProviderConfig, calc *pricing.Calculator) []ModelListItem {
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
	items := buildModelList(h.modelList, h.providerMap, h.calc)

	// Fetch all model usage, using a TTL cache to avoid re-running
	// the aggregation on rapid/concurrent page loads.
	usageMap := h.cachedModelUsageSummaries(r)

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
				item.UpstreamModel = u.UpstreamModel
				item.ProviderName, item.ProviderType, item.Pricing, item.HasPricing = resolveModelPricing(h.providerMap, h.calc, u.ProviderName, u.UpstreamModel)
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
		usage, err = h.store.GetModelUsageSummary(ctx, modelName)
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

	if found != nil {
		view.UpstreamModel = found.UpstreamModel
		view.IsVirtual = len(found.Fallbacks) > 0
		view.IsWildcard = strings.HasSuffix(found.ModelName, "/*")
		view.Fallbacks = found.Fallbacks

		// Resolve fallback details for virtual models.
		if view.IsVirtual {
			view.FallbackDetails = make([]FallbackDetail, len(found.Fallbacks))
			for i, fb := range found.Fallbacks {
				detail := FallbackDetail{
					ModelName:   fb,
					EncodedName: url.PathEscape(fb),
				}
				// Look up the fallback in the model list to get provider/upstream/pricing.
				for j := range h.modelList {
					if h.modelList[j].ModelName == fb {
						m := h.modelList[j]
						if m.Provider != "" {
							detail.UpstreamModel = m.UpstreamModel
							detail.ProviderName, detail.ProviderType, detail.Pricing, detail.HasPricing = resolveModelPricing(h.providerMap, h.calc, m.Provider, m.UpstreamModel)
						}
						break
					}
					// Check wildcard prefix match (e.g. fallback "cm/claude-sonnet-4-6" matches "cm/*").
					if prefix, ok := strings.CutSuffix(h.modelList[j].ModelName, "/*"); ok {
						if strings.HasPrefix(fb, prefix+"/") {
							m := h.modelList[j]
							if m.Provider != "" {
								detail.UpstreamModel = strings.TrimPrefix(fb, prefix+"/")
								detail.ProviderName, detail.ProviderType, detail.Pricing, detail.HasPricing = resolveModelPricing(h.providerMap, h.calc, m.Provider, detail.UpstreamModel)
							}
							break
						}
					}
				}
				view.FallbackDetails[i] = detail
			}
		}
	}

	if found != nil && !view.IsVirtual && found.Provider != "" {
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
		// Format: "MM-DD HH:00" from "YYYY-MM-DD HH"
		lbl := bucket
		if len(bucket) >= 13 {
			lbl = bucket[5:13] + ":00"
		}
		labels = append(labels, ChartLabel{X: x, Label: lbl})
	}

	return points, fill, labels
}
