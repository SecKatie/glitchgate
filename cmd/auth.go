// SPDX-License-Identifier: AGPL-3.0-or-later

package cmd

import (
	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage provider authentication",
	Long:  "Commands for authenticating with upstream LLM providers.",
}

func init() {
	rootCmd.AddCommand(authCmd)
}
