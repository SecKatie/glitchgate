package proxy

import (
	"encoding/json"
	"strings"
)

// RedactRequestBody removes sensitive fields from a request JSON body before
// logging. It strips:
// - "x-api-key" and "authorization" from any nested headers
// - Any field named "api_key" at any level
func RedactRequestBody(body []byte) string {
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		// If we can't parse it, return it as-is (non-JSON body)
		return string(body)
	}
	redactMap(data)
	out, err := json.Marshal(data)
	if err != nil {
		return string(body)
	}
	return string(out)
}

// RedactHeaders creates a copy of headers with sensitive values replaced.
func RedactHeaders(headers map[string][]string) map[string][]string {
	// List of headers to redact (case-insensitive)
	sensitiveHeaders := map[string]bool{
		"x-api-key":       true,
		"authorization":   true,
		"x-proxy-api-key": true,
	}

	result := make(map[string][]string)
	for k, v := range headers {
		if sensitiveHeaders[strings.ToLower(k)] {
			result[k] = []string{"[REDACTED]"}
		} else {
			result[k] = v
		}
	}
	return result
}

func redactMap(m map[string]interface{}) {
	sensitiveKeys := []string{"api_key", "x-api-key", "authorization", "x-proxy-api-key"}
	for k, v := range m {
		lower := strings.ToLower(k)
		for _, sk := range sensitiveKeys {
			if lower == sk {
				m[k] = "[REDACTED]"
			}
		}
		// Recurse into nested maps
		if nested, ok := v.(map[string]interface{}); ok {
			redactMap(nested)
		}
	}
}
