// SPDX-License-Identifier: AGPL-3.0-or-later

// Package cmd implements the llm-proxy CLI commands.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "llm-proxy",
	Short: "LLM API proxy with logging and cost monitoring",
	Long: `llm-proxy is a transparent proxy for LLM APIs that logs all
requests and responses, calculates costs, and provides a web UI
for viewing usage and spending.`,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/.config/llm-proxy/config.yaml)")
}
