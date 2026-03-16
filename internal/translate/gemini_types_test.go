// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeSchemaForGemini_RemovesAdditionalPropertiesRecursively(t *testing.T) {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"location": map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "string"},
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
			},
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"name": map[string]any{"type": "string"},
					},
				},
			},
			"variant": map[string]any{
				"anyOf": []any{
					map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"properties": map[string]any{
							"foo": map[string]any{"type": "string"},
						},
					},
					map[string]any{
						"type":                 "array",
						"additionalProperties": false,
						"items": map[string]any{
							"type":                 "object",
							"additionalProperties": false,
							"properties": map[string]any{
								"bar": map[string]any{"type": "integer"},
							},
						},
					},
				},
			},
		},
	}

	sanitized, ok := sanitizeSchemaForGemini(schema).(map[string]any)
	require.True(t, ok)
	assertNoAdditionalProperties(t, sanitized)
}

func assertNoAdditionalProperties(t *testing.T, value any) {
	t.Helper()

	switch v := value.(type) {
	case map[string]any:
		_, found := v["additionalProperties"]
		require.False(t, found, "additionalProperties should be removed from %#v", v)
		for _, item := range v {
			assertNoAdditionalProperties(t, item)
		}
	case []any:
		for _, item := range v {
			assertNoAdditionalProperties(t, item)
		}
	}
}
