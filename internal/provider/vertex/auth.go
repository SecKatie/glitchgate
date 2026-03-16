// SPDX-License-Identifier: AGPL-3.0-or-later

package vertex

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// newTokenSource creates an OAuth2 token source from either an explicit
// credentials file or Application Default Credentials (ADC). The returned
// token source handles automatic refresh.
func newTokenSource(providerName, credentialsFile string) (oauth2.TokenSource, error) {
	ctx := context.Background()

	var ts oauth2.TokenSource
	if credentialsFile != "" {
		data, err := os.ReadFile(credentialsFile) // #nosec G304 -- path comes from operator-controlled config
		if err != nil {
			return nil, fmt.Errorf("vertex provider %q: reading credentials file: %w", providerName, err)
		}
		creds, err := google.CredentialsFromJSON(ctx, data, cloudPlatformScope) //nolint:staticcheck // SA1019: credentials_file path is operator-controlled config, not untrusted input
		if err != nil {
			return nil, fmt.Errorf("vertex provider %q: parsing credentials: %w", providerName, err)
		}
		ts = creds.TokenSource
	} else {
		creds, err := google.FindDefaultCredentials(ctx, cloudPlatformScope)
		if err != nil {
			return nil, fmt.Errorf("vertex provider %q: finding default credentials: %w", providerName, err)
		}
		ts = creds.TokenSource
	}

	return oauth2.ReuseTokenSource(nil, ts), nil
}
