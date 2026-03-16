package provider

import (
	"fmt"
	"net/url"
)

// ValidateBaseURL checks that a provider base URL has a valid scheme (http or
// https) and a non-empty host. This prevents SSRF via misconfigured or
// attacker-controlled URLs reaching the provider HTTP clients.
func ValidateBaseURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid base URL %q: %w", rawURL, err)
	}
	switch u.Scheme {
	case "http", "https":
		// OK
	default:
		return fmt.Errorf("invalid base URL scheme %q in %q: must be http or https", u.Scheme, rawURL)
	}
	if u.Host == "" {
		return fmt.Errorf("base URL %q has no host", rawURL)
	}
	return nil
}
