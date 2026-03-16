// SPDX-License-Identifier: AGPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/seckatie/glitchgate/internal/app"
	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/store"
)

var costsCmd = &cobra.Command{
	Use:   "costs",
	Short: "View cost summary and breakdown",
	RunE:  runCosts,
}

var (
	costsJSON    bool
	costsFrom    string
	costsTo      string
	costsGroupBy string
	costsFilter  string
)

func init() {
	costsCmd.Flags().BoolVar(&costsJSON, "json", false, "output as JSON")
	costsCmd.Flags().StringVar(&costsFrom, "from", "", "start date (YYYY-MM-DD, default: 30 days ago)")
	costsCmd.Flags().StringVar(&costsTo, "to", "", "end date (YYYY-MM-DD, default: today)")
	costsCmd.Flags().StringVar(&costsGroupBy, "group-by", "model", "group by: model, provider, key")
	costsCmd.Flags().StringVar(&costsFilter, "filter", "", "filter by group name prefix")

	rootCmd.AddCommand(costsCmd)
}

// buildRegistry creates a ProviderRegistry from config for pricing/naming lookups.
// Uses a short timeout since we don't actually make HTTP calls.
func buildRegistry(cfg *config.Config) (*app.ProviderRegistry, error) {
	registry, err := app.NewProviderRegistry(cfg, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("building provider registry: %w", err)
	}
	return registry, nil
}

type costOutput struct {
	From      string             `json:"from"`
	To        string             `json:"to"`
	GroupBy   string             `json:"group_by"`
	TotalCost float64            `json:"total_cost_usd"`
	Summary   costSummaryOut     `json:"summary"`
	Breakdown []costBreakdownRow `json:"breakdown"`
}

type costSummaryOut struct {
	TotalRequests     int64 `json:"total_requests"`
	TotalInputTokens  int64 `json:"total_input_tokens"`
	TotalOutputTokens int64 `json:"total_output_tokens"`
	TotalCacheWrite   int64 `json:"total_cache_write_tokens"`
	TotalCacheRead    int64 `json:"total_cache_read_tokens"`
}

type costBreakdownRow struct {
	Group        string  `json:"group"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheWrite   int64   `json:"cache_write_tokens"`
	CacheRead    int64   `json:"cache_read_tokens"`
	Requests     int64   `json:"requests"`
	CostUSD      float64 `json:"cost_usd"`
}

func runCosts(_ *cobra.Command, _ []string) error {
	st, cfg, err := openDB()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()

	registry, err := buildRegistry(cfg)
	if err != nil {
		return err
	}
	calc := registry.Calculator()

	// Default date range: last 30 days.
	now := time.Now()
	fromDate := costsFrom
	if fromDate == "" {
		fromDate = now.AddDate(0, 0, -30).Format("2006-01-02")
	}
	toDate := costsTo
	if toDate == "" {
		toDate = now.Format("2006-01-02")
	}

	params := store.CostParams{
		From:        fromDate + " 00:00:00",
		To:          toDate + " 23:59:59",
		GroupBy:     costsGroupBy,
		GroupFilter: costsFilter,
		ScopeType:   "all",
	}

	summary, err := st.GetCostSummary(ctx, params)
	if err != nil {
		return fmt.Errorf("querying cost summary: %w", err)
	}

	pricingGroups, err := st.GetCostPricingGroups(ctx, params)
	if err != nil {
		return fmt.Errorf("querying cost breakdown: %w", err)
	}

	// Aggregate breakdown by group and compute costs using the pricing calculator.
	type groupAccum struct {
		inputTokens  int64
		outputTokens int64
		cacheWrite   int64
		cacheRead    int64
		requests     int64
		costUSD      float64
	}

	groups := make(map[string]*groupAccum)
	var groupOrder []string
	var totalCost float64

	for _, g := range pricingGroups {
		groupKey := groupKeyForParams(g, costsGroupBy)

		acc, ok := groups[groupKey]
		if !ok {
			acc = &groupAccum{}
			groups[groupKey] = acc
			groupOrder = append(groupOrder, groupKey)
		}

		acc.inputTokens += g.InputTokens
		acc.outputTokens += g.OutputTokens
		acc.cacheWrite += g.CacheCreationTokens
		acc.cacheRead += g.CacheReadTokens
		acc.requests += g.Requests

		cost := calc.Calculate(g.ProviderName, g.ModelUpstream,
			g.InputTokens, g.OutputTokens, g.CacheCreationTokens,
			g.CacheReadTokens, g.ReasoningTokens)
		if cost != nil {
			acc.costUSD += *cost
			totalCost += *cost
		}
	}

	breakdown := make([]costBreakdownRow, 0, len(groupOrder))
	for _, key := range groupOrder {
		acc := groups[key]
		breakdown = append(breakdown, costBreakdownRow{
			Group:        key,
			InputTokens:  acc.inputTokens,
			OutputTokens: acc.outputTokens,
			CacheWrite:   acc.cacheWrite,
			CacheRead:    acc.cacheRead,
			Requests:     acc.requests,
			CostUSD:      acc.costUSD,
		})
	}

	out := costOutput{
		From:      fromDate,
		To:        toDate,
		GroupBy:   costsGroupBy,
		TotalCost: totalCost,
		Summary: costSummaryOut{
			TotalRequests:     summary.TotalRequests,
			TotalInputTokens:  summary.TotalInputTokens,
			TotalOutputTokens: summary.TotalOutputTokens,
			TotalCacheWrite:   summary.TotalCacheCreationTokens,
			TotalCacheRead:    summary.TotalCacheReadTokens,
		},
		Breakdown: breakdown,
	}

	if costsJSON {
		return printJSON(out)
	}

	fmt.Printf("Cost Summary (%s to %s, grouped by %s)\n\n", fromDate, toDate, costsGroupBy)
	fmt.Printf("  Total Cost:     $%.4f\n", totalCost)
	fmt.Printf("  Requests:       %d\n", summary.TotalRequests)
	fmt.Printf("  Input Tokens:   %s\n", fmtTokens(summary.TotalInputTokens+summary.TotalCacheCreationTokens+summary.TotalCacheReadTokens))
	fmt.Printf("  Output Tokens:  %s\n", fmtTokens(summary.TotalOutputTokens))
	fmt.Println()

	if len(breakdown) == 0 {
		fmt.Println("No cost data found for this date range.")
		return nil
	}

	groupLabel := "MODEL"
	switch costsGroupBy {
	case "provider":
		groupLabel = "PROVIDER"
	case "key":
		groupLabel = "API KEY"
	}

	tw := newTabWriter(os.Stdout)
	_, _ = fmt.Fprintf(tw, "%s\tINPUT\tOUTPUT\tREQUESTS\tCOST\n", groupLabel)
	for _, r := range breakdown {
		cost := "—"
		if r.CostUSD > 0 {
			cost = fmt.Sprintf("$%.4f", r.CostUSD)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n",
			r.Group,
			fmtTokens(r.InputTokens+r.CacheWrite+r.CacheRead),
			fmtTokens(r.OutputTokens),
			r.Requests,
			cost)
	}
	return tw.Flush()
}

func groupKeyForParams(g store.CostPricingGroup, groupBy string) string {
	switch groupBy {
	case "provider":
		return g.ProviderName
	case "key":
		if g.ProxyKeyGroup != "" {
			return g.ProxyKeyGroup
		}
		return g.ProxyKeyPrefix
	default:
		return g.ModelRequested
	}
}
