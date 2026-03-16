// SPDX-License-Identifier: AGPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/seckatie/glitchgate/internal/store"
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Browse request logs",
}

var logsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List request logs",
	RunE:  runLogsList,
}

var logsShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show full detail for a request log entry",
	Args:  cobra.ExactArgs(1),
	RunE:  runLogsShow,
}

var (
	logsJSON    bool
	logsModel   string
	logsStatus  int
	logsKey     string
	logsFrom    string
	logsTo      string
	logsPage    int
	logsPerPage int
	logsSort    string
	logsOrder   string
)

func init() {
	logsListCmd.Flags().BoolVar(&logsJSON, "json", false, "output as JSON")
	logsListCmd.Flags().StringVar(&logsModel, "model", "", "filter by model name")
	logsListCmd.Flags().IntVar(&logsStatus, "status", 0, "filter by HTTP status code")
	logsListCmd.Flags().StringVar(&logsKey, "key", "", "filter by API key prefix")
	logsListCmd.Flags().StringVar(&logsFrom, "from", "", "start date (YYYY-MM-DD)")
	logsListCmd.Flags().StringVar(&logsTo, "to", "", "end date (YYYY-MM-DD)")
	logsListCmd.Flags().IntVar(&logsPage, "page", 1, "page number")
	logsListCmd.Flags().IntVar(&logsPerPage, "per-page", 25, "results per page")
	logsListCmd.Flags().StringVar(&logsSort, "sort", "timestamp", "sort field (timestamp, model, status, latency)")
	logsListCmd.Flags().StringVar(&logsOrder, "order", "desc", "sort order (asc, desc)")

	logsShowCmd.Flags().BoolVar(&logsJSON, "json", false, "output as JSON")

	logsCmd.AddCommand(logsListCmd)
	logsCmd.AddCommand(logsShowCmd)
	rootCmd.AddCommand(logsCmd)
}

type logListOutput struct {
	Logs       []logSummaryRow `json:"logs"`
	Page       int             `json:"page"`
	PerPage    int             `json:"per_page"`
	TotalCount int64           `json:"total_count"`
}

type logSummaryRow struct {
	ID           string `json:"id"`
	Timestamp    string `json:"timestamp"`
	Model        string `json:"model"`
	Provider     string `json:"provider"`
	Status       int    `json:"status"`
	LatencyMs    int64  `json:"latency_ms"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	KeyPrefix    string `json:"key_prefix"`
	Streaming    bool   `json:"streaming"`
}

func runLogsList(_ *cobra.Command, _ []string) error {
	st, _, err := openDB()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	params := store.ListLogsParams{
		Page:      logsPage,
		PerPage:   logsPerPage,
		Model:     logsModel,
		Status:    logsStatus,
		KeyPrefix: logsKey,
		From:      logsFrom,
		To:        logsTo,
		Sort:      logsSort,
		Order:     logsOrder,
		ScopeType: "all",
	}

	logs, totalCount, err := st.ListRequestLogs(ctx, params)
	if err != nil {
		return fmt.Errorf("listing logs: %w", err)
	}

	rows := make([]logSummaryRow, len(logs))
	for i, l := range logs {
		rows[i] = logSummaryRow{
			ID:           l.ID,
			Timestamp:    l.Timestamp.Format("2006-01-02 15:04:05"),
			Model:        l.ModelRequested,
			Provider:     l.ProviderName,
			Status:       l.Status,
			LatencyMs:    l.LatencyMs,
			InputTokens:  l.InputTokens + l.CacheCreationInputTokens + l.CacheReadInputTokens,
			OutputTokens: l.OutputTokens,
			KeyPrefix:    l.ProxyKeyPrefix,
			Streaming:    l.IsStreaming,
		}
	}

	if logsJSON {
		return printJSON(logListOutput{
			Logs:       rows,
			Page:       logsPage,
			PerPage:    logsPerPage,
			TotalCount: totalCount,
		})
	}

	if len(rows) == 0 {
		fmt.Println("No request logs found.")
		return nil
	}

	fmt.Printf("Page %d (%d total)\n\n", logsPage, totalCount)
	tw := newTabWriter(os.Stdout)
	_, _ = fmt.Fprintf(tw, "TIMESTAMP\tMODEL\tSTATUS\tLATENCY\tINPUT\tOUTPUT\tKEY\n")
	for _, r := range rows {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%dms\t%s\t%s\t%s\n",
			r.Timestamp, truncate(r.Model, 30), r.Status,
			r.LatencyMs, fmtTokens(r.InputTokens), fmtTokens(r.OutputTokens),
			r.KeyPrefix)
	}
	return tw.Flush()
}

type logDetailOutput struct {
	ID           string  `json:"id"`
	Timestamp    string  `json:"timestamp"`
	Model        string  `json:"model"`
	Upstream     string  `json:"upstream_model"`
	Provider     string  `json:"provider"`
	Status       int     `json:"status"`
	LatencyMs    int64   `json:"latency_ms"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheWrite   int64   `json:"cache_write_tokens,omitempty"`
	CacheRead    int64   `json:"cache_read_tokens,omitempty"`
	KeyPrefix    string  `json:"key_prefix"`
	Streaming    bool    `json:"streaming"`
	Error        *string `json:"error,omitempty"`
	RequestBody  string  `json:"request_body,omitempty"`
	ResponseBody string  `json:"response_body,omitempty"`
}

func runLogsShow(_ *cobra.Command, args []string) error {
	st, _, err := openDB()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	detail, err := st.GetRequestLog(ctx, args[0])
	if err != nil {
		return fmt.Errorf("getting log detail: %w", err)
	}
	if detail == nil {
		return fmt.Errorf("log entry %q not found", args[0])
	}

	out := logDetailOutput{
		ID:           detail.ID,
		Timestamp:    detail.Timestamp.Format("2006-01-02 15:04:05"),
		Model:        detail.ModelRequested,
		Upstream:     detail.ModelUpstream,
		Provider:     detail.ProviderName,
		Status:       detail.Status,
		LatencyMs:    detail.LatencyMs,
		InputTokens:  detail.InputTokens + detail.CacheCreationInputTokens + detail.CacheReadInputTokens,
		OutputTokens: detail.OutputTokens,
		CacheWrite:   detail.CacheCreationInputTokens,
		CacheRead:    detail.CacheReadInputTokens,
		KeyPrefix:    detail.ProxyKeyPrefix,
		Streaming:    detail.IsStreaming,
		Error:        detail.ErrorDetails,
		RequestBody:  detail.RequestBody,
		ResponseBody: detail.ResponseBody,
	}

	if logsJSON {
		return printJSON(out)
	}

	fmt.Printf("ID:           %s\n", out.ID)
	fmt.Printf("Timestamp:    %s\n", out.Timestamp)
	fmt.Printf("Model:        %s\n", out.Model)
	if out.Upstream != "" {
		fmt.Printf("Upstream:     %s\n", out.Upstream)
	}
	fmt.Printf("Provider:     %s\n", out.Provider)
	fmt.Printf("Status:       %d\n", out.Status)
	fmt.Printf("Latency:      %s\n", strconv.FormatInt(out.LatencyMs, 10)+"ms")
	fmt.Printf("Input Tokens: %s\n", fmtTokens(out.InputTokens))
	fmt.Printf("Output:       %s\n", fmtTokens(out.OutputTokens))
	if out.CacheWrite > 0 {
		fmt.Printf("Cache Write:  %s\n", fmtTokens(out.CacheWrite))
	}
	if out.CacheRead > 0 {
		fmt.Printf("Cache Read:   %s\n", fmtTokens(out.CacheRead))
	}
	fmt.Printf("Key:          %s\n", out.KeyPrefix)
	fmt.Printf("Streaming:    %v\n", out.Streaming)
	if out.Error != nil {
		fmt.Printf("Error:        %s\n", *out.Error)
	}

	if out.RequestBody != "" {
		fmt.Printf("\n--- Request Body ---\n%s\n", out.RequestBody)
	}
	if out.ResponseBody != "" {
		fmt.Printf("\n--- Response Body ---\n%s\n", out.ResponseBody)
	}

	return nil
}
