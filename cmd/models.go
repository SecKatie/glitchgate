// SPDX-License-Identifier: AGPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/store"
)

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "View configured models and usage statistics",
}

var modelsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured models",
	RunE:  runModelsList,
}

var modelsShowCmd = &cobra.Command{
	Use:   "show <model-name>",
	Short: "Show details for a specific model",
	Args:  cobra.ExactArgs(1),
	RunE:  runModelsShow,
}

var modelsJSON bool

func init() {
	modelsListCmd.Flags().BoolVar(&modelsJSON, "json", false, "output as JSON")
	modelsShowCmd.Flags().BoolVar(&modelsJSON, "json", false, "output as JSON")

	modelsCmd.AddCommand(modelsListCmd)
	modelsCmd.AddCommand(modelsShowCmd)
	rootCmd.AddCommand(modelsCmd)
}

type modelListRow struct {
	ModelName     string   `json:"model_name"`
	Provider      string   `json:"provider,omitempty"`
	UpstreamModel string   `json:"upstream_model,omitempty"`
	IsVirtual     bool     `json:"is_virtual,omitempty"`
	IsWildcard    bool     `json:"is_wildcard,omitempty"`
	Fallbacks     []string `json:"fallbacks,omitempty"`
	Requests      int64    `json:"requests"`
	InputTokens   int64    `json:"input_tokens"`
	OutputTokens  int64    `json:"output_tokens"`
	CostUSD       float64  `json:"cost_usd"`
}

func buildProviderMap(cfg *config.Config) map[string]config.ProviderConfig {
	pm := make(map[string]config.ProviderConfig, len(cfg.Providers))
	for _, pc := range cfg.Providers {
		pm[pc.Name] = pc
	}
	return pm
}

func runModelsList(_ *cobra.Command, _ []string) error {
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
	providerMap := buildProviderMap(cfg)

	usageMap, err := st.GetAllModelUsageSummaries(ctx)
	if err != nil {
		return fmt.Errorf("querying model usage: %w", err)
	}

	rows := buildModelRows(cfg, providerMap, calc, usageMap)

	if modelsJSON {
		return printJSON(rows)
	}

	if len(rows) == 0 {
		fmt.Println("No models configured.")
		return nil
	}

	tw := newTabWriter(os.Stdout)
	_, _ = fmt.Fprintf(tw, "MODEL\tPROVIDER\tUPSTREAM\tREQUESTS\tTOKENS\tCOST\n")
	for _, r := range rows {
		totalTokens := r.InputTokens + r.OutputTokens
		cost := "—"
		if r.CostUSD > 0 {
			cost = fmt.Sprintf("$%.4f", r.CostUSD)
		}
		kind := ""
		if r.IsVirtual {
			kind = " (virtual)"
		} else if r.IsWildcard {
			kind = " (wildcard)"
		}
		_, _ = fmt.Fprintf(tw, "%s%s\t%s\t%s\t%d\t%s\t%s\n",
			r.ModelName, kind, r.Provider, r.UpstreamModel,
			r.Requests, fmtTokens(totalTokens), cost)
	}
	return tw.Flush()
}

func runModelsShow(_ *cobra.Command, args []string) error {
	st, cfg, err := openDB()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	modelName := args[0]

	registry, err := buildRegistry(cfg)
	if err != nil {
		return err
	}
	calc := registry.Calculator()
	providerMap := buildProviderMap(cfg)

	usage, err := st.GetModelUsageSummary(ctx, modelName)
	if err != nil {
		return fmt.Errorf("querying model usage: %w", err)
	}

	// Find model in config.
	var mapping *config.ModelMapping
	for _, m := range cfg.ModelList {
		if m.ModelName == modelName {
			mapping = &m
			break
		}
	}

	detail := buildModelDetail(mapping, providerMap, calc, usage, modelName)

	if modelsJSON {
		return printJSON(detail)
	}

	fmt.Printf("Model:     %s\n", detail.ModelName)
	if detail.Provider != "" {
		fmt.Printf("Provider:  %s\n", detail.Provider)
	}
	if detail.UpstreamModel != "" {
		fmt.Printf("Upstream:  %s\n", detail.UpstreamModel)
	}
	if detail.IsVirtual {
		fmt.Printf("Type:      virtual (fallback chain)\n")
		for i, fb := range detail.Fallbacks {
			fmt.Printf("  %d. %s\n", i+1, fb)
		}
	}
	if detail.HasPricing {
		fmt.Printf("\nPricing (per million tokens):\n")
		fmt.Printf("  Input:       $%.2f\n", detail.Pricing.InputPerMillion)
		fmt.Printf("  Output:      $%.2f\n", detail.Pricing.OutputPerMillion)
		if detail.Pricing.CacheWritePerMillion > 0 {
			fmt.Printf("  Cache Write: $%.2f\n", detail.Pricing.CacheWritePerMillion)
		}
		if detail.Pricing.CacheReadPerMillion > 0 {
			fmt.Printf("  Cache Read:  $%.2f\n", detail.Pricing.CacheReadPerMillion)
		}
	}

	if usage != nil {
		fmt.Printf("\nUsage:\n")
		fmt.Printf("  Requests:      %d\n", usage.RequestCount)
		fmt.Printf("  Input Tokens:  %s\n", fmtTokens(usage.InputTokens))
		fmt.Printf("  Output Tokens: %s\n", fmtTokens(usage.OutputTokens))
		if usage.CacheCreationInputTokens > 0 {
			fmt.Printf("  Cache Write:   %s\n", fmtTokens(usage.CacheCreationInputTokens))
		}
		if usage.CacheReadInputTokens > 0 {
			fmt.Printf("  Cache Read:    %s\n", fmtTokens(usage.CacheReadInputTokens))
		}
		if usage.TotalCostUSD > 0 {
			fmt.Printf("  Est. Cost:     $%.4f\n", usage.TotalCostUSD)
		}
	} else {
		fmt.Printf("\nNo usage data found.\n")
	}

	return nil
}

type modelDetail struct {
	ModelName     string                   `json:"model_name"`
	Provider      string                   `json:"provider,omitempty"`
	UpstreamModel string                   `json:"upstream_model,omitempty"`
	IsVirtual     bool                     `json:"is_virtual,omitempty"`
	IsWildcard    bool                     `json:"is_wildcard,omitempty"`
	Fallbacks     []string                 `json:"fallbacks,omitempty"`
	HasPricing    bool                     `json:"has_pricing"`
	Pricing       *pricing.Entry           `json:"pricing,omitempty"`
	Usage         *store.ModelUsageSummary `json:"usage,omitempty"`
}

func buildModelDetail(mapping *config.ModelMapping, providerMap map[string]config.ProviderConfig, calc *pricing.Calculator, usage *store.ModelUsageSummary, modelName string) modelDetail {
	d := modelDetail{
		ModelName: modelName,
		Usage:     usage,
	}

	if mapping != nil {
		d.IsVirtual = len(mapping.Fallbacks) > 0
		d.Fallbacks = mapping.Fallbacks
		if !d.IsVirtual && mapping.Provider != "" {
			d.UpstreamModel = mapping.UpstreamModel
			if pc, ok := providerMap[mapping.Provider]; ok {
				d.Provider = pc.Name
				if e, found := calc.Lookup(pc.Name, mapping.UpstreamModel); found {
					d.Pricing = &e
					d.HasPricing = true
				}
			}
		}
	}

	return d
}

func buildModelRows(cfg *config.Config, providerMap map[string]config.ProviderConfig, _ *pricing.Calculator, usageMap map[string]*store.ModelUsageSummary) []modelListRow {
	seen := make(map[string]bool, len(cfg.ModelList))
	rows := make([]modelListRow, 0, len(cfg.ModelList))

	for _, m := range cfg.ModelList {
		seen[m.ModelName] = true
		row := modelListRow{
			ModelName:  m.ModelName,
			IsVirtual:  len(m.Fallbacks) > 0,
			IsWildcard: len(m.ModelName) > 2 && m.ModelName[len(m.ModelName)-2:] == "/*",
			Fallbacks:  m.Fallbacks,
		}

		if !row.IsVirtual && m.Provider != "" {
			row.UpstreamModel = m.UpstreamModel
			if pc, ok := providerMap[m.Provider]; ok {
				row.Provider = pc.Name
			}
		}

		if u, ok := usageMap[m.ModelName]; ok && u != nil {
			row.Requests = u.RequestCount
			row.InputTokens = u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
			row.OutputTokens = u.OutputTokens
			row.CostUSD = u.TotalCostUSD
		}

		rows = append(rows, row)
	}

	// Add models seen in logs but not in config.
	for name, u := range usageMap {
		if seen[name] || u == nil {
			continue
		}
		rows = append(rows, modelListRow{
			ModelName:    name,
			Requests:     u.RequestCount,
			InputTokens:  u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens,
			OutputTokens: u.OutputTokens,
			CostUSD:      u.TotalCostUSD,
		})
	}

	return rows
}
