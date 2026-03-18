// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
)

// defaultMaxTokens is the fallback value when the client doesn't specify
// max_tokens. Anthropic requires this field.
const defaultMaxTokens = 4096

// marshalToolInput marshals tool input to JSON bytes for use as function call
// arguments. Returns "{}" on nil input or marshal failure.
func marshalToolInput(input any) []byte {
	if input == nil {
		return []byte("{}")
	}
	data, err := json.Marshal(input)
	if err != nil {
		slog.Warn("failed to marshal tool input, defaulting to {}", "error", err)
		return []byte("{}")
	}
	return data
}

// unmarshalToolArgs unmarshals a JSON arguments string into an interface value
// suitable for Anthropic's tool_use input field. Returns an empty map on empty
// string or unmarshal failure.
func unmarshalToolArgs(args string) any {
	if args == "" {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal([]byte(args), &v); err != nil {
		slog.Warn("failed to unmarshal tool arguments, defaulting to {}", "error", err)
		return map[string]any{}
	}
	return v
}

// unmarshalSchema unmarshals a json.RawMessage (tool parameters or input
// schema) into an interface value. Returns an empty map on nil/empty input or
// unmarshal failure.
func unmarshalSchema(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		slog.Warn("failed to unmarshal tool schema, defaulting to {}", "error", err)
		return map[string]any{}
	}
	return v
}

// escapeJSON escapes a string for inclusion in a JSON string literal.
func escapeJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return s
	}
	// Remove the surrounding quotes added by json.Marshal.
	return string(b[1 : len(b)-1])
}

// parseStopSequences handles the OpenAI stop field which can be a string
// or an array of strings.
func parseStopSequences(stop interface{}) ([]string, error) {
	switch v := stop.(type) {
	case string:
		if v != "" {
			return []string{v}, nil
		}
		return nil, nil
	case []interface{}:
		var seqs []string
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("stop array must contain strings")
			}
			seqs = append(seqs, s)
		}
		return seqs, nil
	case []string:
		return v, nil
	default:
		// Try JSON re-marshal/unmarshal for complex cases.
		data, err := json.Marshal(stop)
		if err != nil {
			return nil, fmt.Errorf("cannot parse stop field")
		}
		var s string
		if json.Unmarshal(data, &s) == nil {
			return []string{s}, nil
		}
		var arr []string
		if json.Unmarshal(data, &arr) == nil {
			return arr, nil
		}
		return nil, fmt.Errorf("stop must be a string or array of strings")
	}
}

// extractTextContent extracts a plain text string from an OpenAI content
// field, which may be a string or an array of openai.ContentPart objects.
func extractTextContent(content interface{}) (string, error) {
	switch v := content.(type) {
	case string:
		return v, nil
	case []interface{}:
		var parts []string
		for _, item := range v {
			m, ok := item.(map[string]interface{})
			if !ok {
				return "", fmt.Errorf("content part must be an object")
			}
			if t, ok := m["type"].(string); ok && t == "text" {
				if text, ok := m["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, ""), nil
	case nil:
		return "", nil
	default:
		return "", fmt.Errorf("unsupported content type")
	}
}

// mapStopReason translates an Anthropic stop_reason to an OpenAI finish_reason.
func mapStopReason(stopReason *string) *string {
	if stopReason == nil {
		return nil
	}
	var reason string
	switch *stopReason {
	case "end_turn":
		reason = "stop"
	case "max_tokens":
		reason = "length"
	case "stop_sequence":
		reason = "stop"
	case "tool_use":
		reason = "tool_calls"
	default:
		reason = *stopReason
	}
	return &reason
}

// mapAnthropicErrorType maps Anthropic error types to OpenAI error types.
func mapAnthropicErrorType(errType string) string {
	switch errType {
	case "authentication_error":
		return "authentication_error"
	case "invalid_request_error":
		return "invalid_request_error"
	case "not_found_error":
		return "not_found_error"
	case "rate_limit_error":
		return "rate_limit_error"
	case "overloaded_error":
		return "server_error"
	default:
		return "api_error"
	}
}

// extractAnthropicSystem extracts a plain text string from the Anthropic system
// field, which can be a string or an array of system blocks. Anthropic-internal
// metadata blocks (e.g. x-anthropic-billing-header) are dropped because they
// contain per-request hashes that defeat upstream prefix caching.
func extractAnthropicSystem(system interface{}) string {
	switch v := system.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				if t, ok := m["type"].(string); ok && t == "text" {
					if text, ok := m["text"].(string); ok {
						if strings.HasPrefix(text, "x-anthropic-") {
							continue
						}
						parts = append(parts, text)
					}
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

// parseContentBlocks extracts ContentBlock structs from an interface{} that
// may be a JSON array of content blocks.
func parseContentBlocks(content interface{}) ([]anthropic.ContentBlock, error) {
	raw, err := json.Marshal(content)
	if err != nil {
		return nil, fmt.Errorf("marshalling content: %w", err)
	}

	var blocks []anthropic.ContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("parsing content blocks: %w", err)
	}
	return blocks, nil
}

// sanitizeAnthropicToolID replaces any character not in [a-zA-Z0-9_-] with '_'
// and truncates to 64 characters, satisfying Anthropic's tool_use ID constraints.
func sanitizeAnthropicToolID(id string) string {
	const maxLen = 64
	b := []byte(id)
	for i, c := range b {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			b[i] = '_'
		}
	}
	if len(b) > maxLen {
		b = b[:maxLen]
	}
	return string(b)
}

// extractToolResultText extracts a plain text string from a tool_result
// ContentBlock. The content field may be a string, an array of content blocks,
// or absent (fall back to Text).
func extractToolResultText(b anthropic.ContentBlock) string {
	if b.Content != nil {
		switch v := b.Content.(type) {
		case string:
			return v
		case []interface{}:
			// Array of content blocks — collect text parts.
			var parts []string
			for _, item := range v {
				if m, ok := item.(map[string]interface{}); ok {
					if t, ok := m["type"].(string); ok && t == "text" {
						if text, ok := m["text"].(string); ok {
							parts = append(parts, text)
						}
					}
				}
			}
			return strings.Join(parts, "")
		}
	}
	// Fallback: some clients put content directly in Text.
	return b.Text
}

// effortToBudgetTokens maps an OpenAI reasoning_effort string to an
// Anthropic thinking budget_tokens value. The budget is capped to
// maxTokens-1 since Anthropic requires budget_tokens < max_tokens.
func effortToBudgetTokens(effort string, maxTokens int) int {
	var budget int
	switch effort {
	case "low":
		budget = 1024
	case "medium":
		budget = 5000
	case "high":
		budget = 10000
	default:
		budget = 5000
	}
	if maxTokens > 0 && budget >= maxTokens {
		budget = maxTokens - 1
	}
	if budget < 1 {
		budget = 1
	}
	return budget
}

// budgetTokensToEffort maps an Anthropic thinking budget_tokens value
// to an OpenAI reasoning_effort string.
func budgetTokensToEffort(budget int) string {
	switch {
	case budget <= 2000:
		return "low"
	case budget <= 7000:
		return "medium"
	default:
		return "high"
	}
}
