// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"fmt"

	"codeberg.org/kglitchy/glitchgate/internal/provider/anthropic"
)

// AnthropicToResponses translates an Anthropic MessagesRequest to a Responses API request.
func AnthropicToResponses(req *anthropic.MessagesRequest, upstreamModel string) (*ResponsesRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request must not be nil")
	}

	result := &ResponsesRequest{
		Model:       upstreamModel,
		Temperature: req.Temperature,
		TopP:        req.TopP,
	}

	if req.Stream {
		streaming := true
		result.Stream = &streaming
	}

	if req.MaxTokens > 0 {
		mt := req.MaxTokens
		result.MaxOutputTokens = &mt
	}

	// Map system to instructions.
	if req.System != nil {
		switch s := req.System.(type) {
		case string:
			if s != "" {
				result.Instructions = &s
			}
		}
	}

	// Convert messages to Responses API input items.
	var items []InputItem
	for _, msg := range req.Messages {
		msgItems, err := anthropicMessageToResponsesInput(msg)
		if err != nil {
			return nil, fmt.Errorf("translating message: %w", err)
		}
		items = append(items, msgItems...)
	}

	if len(items) > 0 {
		inputJSON, err := json.Marshal(items)
		if err != nil {
			return nil, fmt.Errorf("marshalling input: %w", err)
		}
		result.Input = inputJSON
	}

	// Translate tools.
	for _, t := range req.Tools {
		var params json.RawMessage
		if t.InputSchema != nil {
			p, err := json.Marshal(t.InputSchema)
			if err != nil {
				return nil, fmt.Errorf("marshalling tool %q input schema: %w", t.Name, err)
			}
			params = p
		}
		result.Tools = append(result.Tools, ResponsesTool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  params,
		})
	}

	// Translate tool_choice.
	if req.ToolChoice != nil {
		tc, err := anthropicToolChoiceToResponses(req.ToolChoice)
		if err != nil {
			return nil, err
		}
		result.ToolChoice = tc
	}

	// Translate Anthropic thinking config to Responses reasoning.
	if req.Thinking != nil && req.Thinking.Type == "enabled" {
		effort := budgetTokensToEffort(req.Thinking.BudgetTokens)
		summary := "auto"
		result.Reasoning = &Reasoning{
			Effort:  &effort,
			Summary: &summary,
		}
	}

	return result, nil
}

// anthropicMessageToResponsesInput converts an Anthropic message to Responses API input items.
func anthropicMessageToResponsesInput(msg anthropic.Message) ([]InputItem, error) {
	role := msg.Role

	// Handle string content.
	if text, ok := msg.Content.(string); ok {
		return []InputItem{{
			Type:    "message",
			Role:    role,
			Content: json.RawMessage(`"` + escapeJSON(text) + `"`),
		}}, nil
	}

	// Handle content block array.
	blocks, ok := msg.Content.([]anthropic.ContentBlock)
	if !ok {
		// Try to parse from JSON.
		raw, err := json.Marshal(msg.Content)
		if err != nil {
			return nil, fmt.Errorf("unsupported content type")
		}
		if err := json.Unmarshal(raw, &blocks); err != nil {
			// Try as string.
			var text string
			if err := json.Unmarshal(raw, &text); err == nil {
				return []InputItem{{
					Type:    "message",
					Role:    role,
					Content: json.RawMessage(`"` + escapeJSON(text) + `"`),
				}}, nil
			}
			return nil, fmt.Errorf("unsupported content format")
		}
	}

	var items []InputItem
	for _, block := range blocks {
		switch block.Type {
		case "text":
			items = append(items, InputItem{
				Type:    "message",
				Role:    role,
				Content: json.RawMessage(`[{"type":"input_text","text":"` + escapeJSON(block.Text) + `"}]`),
			})

		case "tool_use":
			args, err := json.Marshal(block.Input)
			if err != nil {
				args = []byte("{}")
			}
			items = append(items, InputItem{
				Type:      "function_call",
				CallID:    block.ID,
				Name:      block.Name,
				Arguments: string(args),
			})

		case "tool_result":
			items = append(items, InputItem{
				Type:   "function_call_output",
				CallID: block.ID,
				Output: block.Text,
			})
		}
	}

	return items, nil
}

// anthropicToolChoiceToResponses converts Anthropic tool_choice to Responses API format.
func anthropicToolChoiceToResponses(tc interface{}) (interface{}, error) {
	switch v := tc.(type) {
	case map[string]string:
		switch v["type"] {
		case "auto":
			return "auto", nil
		case "any":
			return "required", nil
		case "tool":
			if name, ok := v["name"]; ok {
				return map[string]interface{}{"type": "function", "name": name}, nil
			}
			return "auto", nil
		default:
			return "auto", nil
		}
	case map[string]interface{}:
		t, _ := v["type"].(string)
		switch t {
		case "auto":
			return "auto", nil
		case "any":
			return "required", nil
		case "tool":
			if name, ok := v["name"].(string); ok {
				return map[string]interface{}{"type": "function", "name": name}, nil
			}
			return "auto", nil
		default:
			return "auto", nil
		}
	default:
		return nil, fmt.Errorf("unsupported tool_choice type")
	}
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
