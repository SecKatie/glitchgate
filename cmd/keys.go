// SPDX-License-Identifier: AGPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"codeberg.org/kglitchy/glitchgate/internal/auth"
	"codeberg.org/kglitchy/glitchgate/internal/config"
	"codeberg.org/kglitchy/glitchgate/internal/store"
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

var keysDeleteCmd = &cobra.Command{
	Use:   "delete <prefix>",
	Short: "Delete a proxy API key by prefix",
	Args:  cobra.ExactArgs(1),
	RunE:  runKeysDelete,
}

var keysUpdateCmd = &cobra.Command{
	Use:   "update <prefix>",
	Short: "Update a proxy API key's label",
	Args:  cobra.ExactArgs(1),
	RunE:  runKeysUpdate,
}

var (
	keyLabel    string
	keyNewLabel string
)

func init() {
	keysCreateCmd.Flags().StringVar(&keyLabel, "label", "", "human-readable label for the key (required)")
	if err := keysCreateCmd.MarkFlagRequired("label"); err != nil {
		panic(fmt.Sprintf("mark label flag required: %v", err))
	}

	keysUpdateCmd.Flags().StringVar(&keyNewLabel, "label", "", "new label for the key (required)")
	if err := keysUpdateCmd.MarkFlagRequired("label"); err != nil {
		panic(fmt.Sprintf("mark label flag required: %v", err))
	}

	keysCmd.AddCommand(keysCreateCmd)
	keysCmd.AddCommand(keysListCmd)
	keysCmd.AddCommand(keysDeleteCmd)
	keysCmd.AddCommand(keysUpdateCmd)
	rootCmd.AddCommand(keysCmd)
}

// keyStore combines the store operations needed by CLI key management commands.
type keyStore interface {
	store.ProxyKeyStore
	RecordAuditEvent(ctx context.Context, action, keyPrefix, detail string) error
	Migrate(ctx context.Context) error
	Close() error
}

func openStore() (keyStore, error) {
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

	if err := st.RecordAuditEvent(context.Background(), "key_created", prefix, keyLabel); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: record audit event: %v\n", err)
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

func runKeysDelete(_ *cobra.Command, args []string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	prefix := args[0]
	if err := st.RevokeProxyKey(context.Background(), prefix); err != nil {
		return fmt.Errorf("revoking key: %w", err)
	}

	if err := st.RecordAuditEvent(context.Background(), "key_revoked", prefix, ""); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: record audit event: %v\n", err)
	}

	fmt.Printf("Deleted key with prefix: %s\n", prefix)
	return nil
}

func runKeysUpdate(_ *cobra.Command, args []string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	prefix := args[0]
	if keyNewLabel == "" {
		return fmt.Errorf("label is required")
	}
	if len(keyNewLabel) > 64 {
		return fmt.Errorf("label must be 64 characters or fewer")
	}

	if err := st.UpdateKeyLabel(context.Background(), prefix, keyNewLabel); err != nil {
		return fmt.Errorf("updating key label: %w", err)
	}

	fmt.Printf("Updated label for key %s: %s\n", prefix, keyNewLabel)
	return nil
}
