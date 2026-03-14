// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeRedirect(t *testing.T) {
	tests := []struct {
		name string
		dest string
		want string
	}{
		{"empty", "", ""},
		{"local path", "/ui/models", "/ui/models"},
		{"root", "/", "/"},
		{"local with query", "/ui/costs?from=2026-01-01", "/ui/costs?from=2026-01-01"},
		{"protocol relative", "//evil.com", ""},
		{"full URL", "https://evil.com/steal", ""},
		{"backslash bypass", "/\\evil.com", ""},
		{"scheme in path", "/foo://bar", ""},
		{"no leading slash", "evil.com", ""},
		{"javascript scheme", "javascript:alert(1)", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, sanitizeRedirect(tc.dest))
		})
	}
}
