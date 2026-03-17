// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
)

// TestCompareCachePrefixes loads two consecutive Anthropic requests from /tmp,
// translates them both to OpenAI format, serializes to JSON, and compares the
// shared prefix byte-by-byte to find what breaks caching.
func TestCompareCachePrefixes(t *testing.T) {
	aPath := "/tmp/req_good.json"
	bPath := "/tmp/req_bad.json"

	if _, err := os.Stat(aPath); err != nil {
		t.Skipf("skipping: %s not found", aPath)
	}
	if _, err := os.Stat(bPath); err != nil {
		t.Skipf("skipping: %s not found", bPath)
	}

	aJSON := loadAndTranslate(t, aPath)
	bJSON := loadAndTranslate(t, bPath)

	t.Logf("req A (earlier) JSON length: %d bytes", len(aJSON))
	t.Logf("req B (later)   JSON length: %d bytes", len(bJSON))

	// Find where they diverge.
	minLen := len(aJSON)
	if len(bJSON) < minLen {
		minLen = len(bJSON)
	}

	divergeAt := -1
	for i := 0; i < minLen; i++ {
		if aJSON[i] != bJSON[i] {
			divergeAt = i
			break
		}
	}

	if divergeAt == -1 && len(aJSON) == len(bJSON) {
		t.Log("requests are byte-identical (unexpected for different turns)")
		return
	}

	if divergeAt == -1 {
		divergeAt = minLen
		t.Logf("shorter request is a prefix of longer — perfect cache prefix (%d bytes)", divergeAt)
		return
	}

	t.Logf("divergence at byte %d (%.1f%% of req A)", divergeAt, float64(divergeAt)/float64(len(aJSON))*100)

	// Show context around divergence.
	start := divergeAt - 300
	if start < 0 {
		start = 0
	}
	endA := divergeAt + 300
	if endA > len(aJSON) {
		endA = len(aJSON)
	}
	endB := divergeAt + 300
	if endB > len(bJSON) {
		endB = len(bJSON)
	}

	t.Logf("req A around divergence [%d:%d]:\n%s", start, endA, string(aJSON[start:endA]))
	t.Logf("req B around divergence [%d:%d]:\n%s", start, endB, string(bJSON[start:endB]))

	// Also write the translated JSON to files for manual inspection.
	_ = os.WriteFile("/tmp/translated_a.json", aJSON, 0o600) // #nosec G303 -- temp files for debug output
	_ = os.WriteFile("/tmp/translated_b.json", bJSON, 0o600) // #nosec G303 -- temp files for debug output
	t.Log("wrote /tmp/translated_a.json and /tmp/translated_b.json")
}

func loadAndTranslate(t *testing.T, path string) []byte {
	t.Helper()

	// #nosec G304 -- path is from constants at call site (/tmp req fixtures)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	var req anthropic.MessagesRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	oai, err := AnthropicToOpenAIRequest(&req)
	if err != nil {
		t.Fatalf("translating %s: %v", path, err)
	}

	out, err := json.Marshal(oai)
	if err != nil {
		t.Fatalf("marshalling %s: %v", path, err)
	}

	return out
}
