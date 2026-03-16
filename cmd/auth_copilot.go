// SPDX-License-Identifier: AGPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/provider/copilot"
)

var (
	copilotTokenDir     string
	copilotProviderName string
	copilotForceReauth  bool
)

var authCopilotCmd = &cobra.Command{
	Use:   "copilot",
	Short: "Authenticate with GitHub Copilot via device flow",
	Long: `Initiates the GitHub OAuth device flow to obtain a Copilot API token.
This command can be run independently of the proxy server — before the
provider is configured or while the proxy is already running.`,
	RunE: runAuthCopilot,
}

func init() {
	authCmd.AddCommand(authCopilotCmd)
	authCopilotCmd.Flags().StringVar(&copilotTokenDir, "token-dir", "", "token storage directory (default: ~/.config/glitchgate/copilot/)")
	authCopilotCmd.Flags().StringVarP(&copilotProviderName, "name", "n", "", "provider name from config (looks up its token_dir; mutually exclusive with --token-dir)")
	authCopilotCmd.Flags().BoolVarP(&copilotForceReauth, "force", "f", false, "re-authenticate even if tokens already exist")
	authCopilotCmd.MarkFlagsMutuallyExclusive("token-dir", "name")
}

func runAuthCopilot(_ *cobra.Command, _ []string) error {
	tokenDir, err := resolveTokenDir()
	if err != nil {
		return err
	}

	// Check if already authenticated (unless --force is set).
	if !copilotForceReauth {
		if token, err := copilot.LoadGitHubToken(tokenDir); err == nil && token.AccessToken != "" {
			fmt.Println("Already authenticated with GitHub Copilot.")
			fmt.Printf("Tokens stored at: %s\n", tokenDir)
			fmt.Println("Use --force to re-authenticate.")
			return nil
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Step 1: Request device code.
	fmt.Println("Requesting device code from GitHub...")
	deviceResp, err := copilot.RequestDeviceCode(ctx)
	if err != nil {
		return fmt.Errorf("device code request failed: %w", err)
	}

	fmt.Println()
	fmt.Println("To authenticate with GitHub Copilot, visit:")
	fmt.Printf("  %s\n", deviceResp.VerificationURI)
	fmt.Println()
	fmt.Printf("Enter code: %s\n", deviceResp.UserCode)
	fmt.Println()
	fmt.Println("Waiting for authorization...")

	// Step 2: Poll for access token.
	ghToken, err := copilot.PollForAccessToken(ctx, deviceResp.DeviceCode, deviceResp.Interval, deviceResp.ExpiresIn)
	if err != nil {
		return fmt.Errorf("authorization failed: %w", err)
	}

	// Step 3: Save GitHub token.
	if err := copilot.SaveGitHubToken(tokenDir, ghToken); err != nil {
		return fmt.Errorf("saving GitHub token: %w", err)
	}

	// Step 4: Exchange for Copilot session token.
	fmt.Println("Exchanging for Copilot API token...")
	sessionToken, err := copilot.ExchangeForCopilotToken(ctx, ghToken.AccessToken)
	if err != nil {
		// GitHub token is saved; session token can be obtained later.
		fmt.Fprintf(os.Stderr, "Warning: Copilot token exchange failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "GitHub token saved. The proxy will retry the exchange on startup.\n")
	} else {
		if err := copilot.SaveCopilotToken(tokenDir, sessionToken); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: saving Copilot token cache: %v\n", err)
		}
	}

	fmt.Println()
	fmt.Printf("Authorization successful. Tokens saved to %s\n", tokenDir)
	return nil
}

// resolveTokenDir determines the token directory from flags or config.
func resolveTokenDir() (string, error) {
	if copilotProviderName != "" {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return "", fmt.Errorf("loading config: %w", err)
		}
		pc, err := cfg.FindProvider(copilotProviderName)
		if err != nil {
			return "", err
		}
		if pc.Type != "github_copilot" {
			return "", fmt.Errorf("provider %q has type %q, not github_copilot", copilotProviderName, pc.Type)
		}
		if pc.TokenDir == "" {
			return "", fmt.Errorf("provider %q has no token_dir configured; set token_dir or use --token-dir", copilotProviderName)
		}
		return pc.TokenDir, nil
	}

	tokenDir := copilotTokenDir
	if tokenDir == "" {
		tokenDir = copilot.DefaultTokenDir()
	}
	if tokenDir == "" {
		return "", fmt.Errorf("unable to determine home directory for token storage; use --token-dir or --name")
	}
	return tokenDir, nil
}
