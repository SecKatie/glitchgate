// SPDX-License-Identifier: AGPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"codeberg.org/kglitchy/llm-proxy/internal/auth"
	"codeberg.org/kglitchy/llm-proxy/internal/config"
	"codeberg.org/kglitchy/llm-proxy/internal/store"
)

var keysCmd = &cobra.Command{
	Use:   "keys",
	Short: "Manage proxy API keys",
}

var keysCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new proxy API key",
	RunE:  runKeysCreate,
}

var keysListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all active proxy API keys",
	RunE:  runKeysList,
}

var keysRevokeCmd = &cobra.Command{
	Use:   "revoke <prefix>",
	Short: "Revoke a proxy API key by prefix",
	Args:  cobra.ExactArgs(1),
	RunE:  runKeysRevoke,
}

var keyLabel string

func init() {
	keysCreateCmd.Flags().StringVar(&keyLabel, "label", "", "human-readable label for the key (required)")
	if err := keysCreateCmd.MarkFlagRequired("label"); err != nil {
		panic(fmt.Sprintf("mark label flag required: %v", err))
	}

	keysCmd.AddCommand(keysCreateCmd)
	keysCmd.AddCommand(keysListCmd)
	keysCmd.AddCommand(keysRevokeCmd)
	rootCmd.AddCommand(keysCmd)
}

func openStore() (store.Store, error) {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	st, err := store.NewSQLiteStore(cfg.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}
	return st, nil
}

func runKeysCreate(_ *cobra.Command, _ []string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	plaintext, hash, prefix, err := auth.GenerateKey()
	if err != nil {
		return fmt.Errorf("generating key: %w", err)
	}

	id := uuid.New().String()
	if err := st.CreateProxyKey(context.Background(), id, hash, prefix, keyLabel); err != nil {
		return fmt.Errorf("storing key: %w", err)
	}

	fmt.Printf("Created API key: %s\n", plaintext)
	fmt.Printf("Label: %s\n", keyLabel)
	fmt.Printf("Prefix: %s\n", prefix)
	fmt.Println("\nSave this key now — it will not be shown again.")
	return nil
}

func runKeysList(_ *cobra.Command, _ []string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	keys, err := st.ListActiveProxyKeys(context.Background())
	if err != nil {
		return fmt.Errorf("listing keys: %w", err)
	}

	if len(keys) == 0 {
		fmt.Println("No active API keys.")
		return nil
	}

	fmt.Printf("%-14s %-20s %s\n", "PREFIX", "LABEL", "CREATED")
	for _, k := range keys {
		fmt.Printf("%-14s %-20s %s\n", k.KeyPrefix, k.Label, k.CreatedAt.Format("2006-01-02 15:04"))
	}
	return nil
}

func runKeysRevoke(_ *cobra.Command, args []string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	prefix := args[0]
	if err := st.RevokeProxyKey(context.Background(), prefix); err != nil {
		return fmt.Errorf("revoking key: %w", err)
	}

	fmt.Printf("Revoked key with prefix: %s\n", prefix)
	return nil
}
