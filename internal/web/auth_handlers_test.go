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
		// Path traversal cases
		{"traversal to admin", "/ui/../admin", ""},
		{"traversal to secret", "/ui/../secret/file", ""},
		{"double traversal still under ui", "/ui/../valid/../ui/index.html", ""},
		{"traversal from root", "/../etc/passwd", ""},
		{"dot-dot only", "/..", ""},
		{"encoded traversal", "/ui/%2e%2e/admin", "/ui/%2e%2e/admin"}, // path.Clean does not decode; raw dots checked
		{"traversal with query", "/ui/../admin?foo=bar", ""},
		{"clean path with query preserved", "/ui/models?from=2026-01-01", "/ui/models?from=2026-01-01"},
		{"trailing slash cleaned", "/ui/models/", "/ui/models"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, sanitizeRedirect(tc.dest))
		})
	}
}
