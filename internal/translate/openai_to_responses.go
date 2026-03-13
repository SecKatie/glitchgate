// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"fmt"
)

// OpenAIToResponses translates a Chat Completions request to a Responses API request.
func OpenAIToResponses(req *ChatCompletionRequest, upstreamModel string) (*ResponsesRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request must not be nil")
	}

	result := &ResponsesRequest{
		Model:           upstreamModel,
		Temperature:     req.Temperature,
		TopP:            req.TopP,
		MaxOutputTokens: req.MaxTokens,
	}

	if req.Stream {
		streaming := true
		result.Stream = &streaming
	}

	// Convert messages to Responses API input items.
	var items []InputItem
	for _, msg := range req.Messages {
		switch msg.Role {
		case "system":
			// Map system messages to instructions.
			if text, ok := msg.Content.(string); ok {
				if result.Instructions == nil {
					result.Instructions = &text
				} else {
					combined := *result.Instructions + "\n" + text
					result.Instructions = &combined
				}
			}

		case "user":
			item := InputItem{
				Type: "message",
				Role: "user",
			}
			if text, ok := msg.Content.(string); ok {
				item.Content = json.RawMessage(`"` + escapeJSON(text) + `"`)
			} else {
				// Multipart content — marshal as-is and convert to Responses input types.
				raw, err := json.Marshal(msg.Content)
				if err != nil {
					return nil, fmt.Errorf("marshalling user content: %w", err)
				}
				item.Content = raw
			}
			items = append(items, item)

		case "assistant":
			if content, ok, err := openAIMessageContentToRaw(msg.Content); err != nil {
				return nil, fmt.Errorf("marshalling assistant content: %w", err)
			} else if ok {
				items = append(items, InputItem{
					Type:    "message",
					Role:    "assistant",
					Content: content,
				})
			}

			// Chat Completions assistant messages can include both content and tool calls.
			for _, tc := range msg.ToolCalls {
				items = append(items, InputItem{
					Type:      "function_call",
					CallID:    tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}

		case "tool":
			items = append(items, InputItem{
				Type:   "function_call_output",
				CallID: msg.ToolCallID,
				Output: fmt.Sprintf("%v", msg.Content),
			})
		}
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
		if t.Function.Parameters != nil {
			p, err := json.Marshal(t.Function.Parameters)
			if err == nil {
				params = p
			}
		}
		result.Tools = append(result.Tools, ResponsesTool{
			Type:        "function",
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  params,
		})
	}

	// Translate tool_choice.
	if req.ToolChoice != nil {
		tc, err := openAIToolChoiceToResponses(req.ToolChoice)
		if err != nil {
			return nil, err
		}
		result.ToolChoice = tc
	}

	return result, nil
}

func openAIMessageContentToRaw(content interface{}) (json.RawMessage, bool, error) {
	switch v := content.(type) {
	case nil:
		return nil, false, nil
	case string:
		if v == "" {
			return nil, false, nil
		}
		return json.RawMessage(`"` + escapeJSON(v) + `"`), true, nil
	default:
		raw, err := json.Marshal(content)
		if err != nil {
			return nil, false, err
		}
		if string(raw) == "null" || string(raw) == "[]" {
			return nil, false, nil
		}
		return raw, true, nil
	}
}

// openAIToolChoiceToResponses converts CC tool_choice to Responses API format.
func openAIToolChoiceToResponses(tc interface{}) (interface{}, error) {
	switch v := tc.(type) {
	case string:
		switch v {
		case "none", "auto", "required":
			return v, nil
		default:
			return nil, fmt.Errorf("unsupported tool_choice value: %s", v)
		}
	case map[string]interface{}:
		if fn, ok := v["function"].(map[string]interface{}); ok {
			if name, ok := fn["name"].(string); ok {
				return map[string]interface{}{"type": "function", "name": name}, nil
			}
		}
		return "auto", nil
	default:
		return nil, fmt.Errorf("unsupported tool_choice type")
	}
}
