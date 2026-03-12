// SPDX-License-Identifier: AGPL-3.0-or-later

package copilot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	githubTokenFile  = "github_token.json"  // #nosec G101 -- not a credential, just a filename
	copilotTokenFile = "copilot_token.json" // #nosec G101 -- not a credential, just a filename
	dirPermissions   = 0o700
	filePermissions  = 0o600
)

// DefaultTokenDir returns the default directory for storing Copilot tokens.
func DefaultTokenDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "glitchgate", "copilot")
}

// EnsureTokenDir creates the token directory with 0700 permissions if it does not exist.
func EnsureTokenDir(dir string) error {
	return os.MkdirAll(dir, dirPermissions)
}

// SaveGitHubToken writes the GitHub OAuth token to disk with 0600 permissions.
func SaveGitHubToken(dir string, token *GitHubToken) error {
	if err := EnsureTokenDir(dir); err != nil {
		return fmt.Errorf("creating token directory: %w", err)
	}
	return writeJSON(filepath.Join(dir, githubTokenFile), token)
}

// LoadGitHubToken reads the GitHub OAuth token from disk.
func LoadGitHubToken(dir string) (*GitHubToken, error) {
	var token GitHubToken
	if err := readJSON(filepath.Join(dir, githubTokenFile), &token); err != nil {
		return nil, err
	}
	if token.AccessToken == "" {
		return nil, fmt.Errorf("github token file is empty or invalid")
	}
	return &token, nil
}

// SaveCopilotToken writes the Copilot session token to disk with 0600 permissions.
func SaveCopilotToken(dir string, token *SessionToken) error {
	if err := EnsureTokenDir(dir); err != nil {
		return fmt.Errorf("creating token directory: %w", err)
	}
	return writeJSON(filepath.Join(dir, copilotTokenFile), token)
}

// LoadCopilotToken reads the Copilot session token from disk.
func LoadCopilotToken(dir string) (*SessionToken, error) {
	var token SessionToken
	if err := readJSON(filepath.Join(dir, copilotTokenFile), &token); err != nil {
		return nil, err
	}
	return &token, nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling token: %w", err)
	}
	if err := os.WriteFile(path, data, filePermissions); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path) // #nosec G304 -- path is from user-configured token directory
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	return nil
}
