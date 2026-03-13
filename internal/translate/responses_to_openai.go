// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"fmt"
)

// ResponsesToOpenAI translates a Responses API request to an OpenAI Chat Completions request.
func ResponsesToOpenAI(req *ResponsesRequest, upstreamModel string) (*ChatCompletionRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request must not be nil")
	}

	result := &ChatCompletionRequest{
		Model:       upstreamModel,
		Stream:      req.Stream != nil && *req.Stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		MaxTokens:   req.MaxOutputTokens,
	}

	// Parse input and convert to Chat Completions messages.
	messages, err := responsesInputToOpenAIMessages(req.Input, req.Instructions)
	if err != nil {
		return nil, fmt.Errorf("translating input: %w", err)
	}
	result.Messages = messages

	// Translate tools.
	for _, t := range req.Tools {
		if t.Type != "function" {
			return nil, fmt.Errorf("tool type %q is not supported for Chat Completions upstream; only function tools are translatable", t.Type)
		}
		result.Tools = append(result.Tools, OpenAITool{
			Type: "function",
			Function: ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	// Translate tool_choice.
	if req.ToolChoice != nil {
		tc, err := responsesToolChoiceToOpenAI(req.ToolChoice)
		if err != nil {
			return nil, err
		}
		result.ToolChoice = tc
	}

	// Forward reasoning effort.
	if req.Reasoning != nil && req.Reasoning.Effort != nil && *req.Reasoning.Effort != "" {
		result.ReasoningEffort = req.Reasoning.Effort
	}

	// Map text.format to response_format (if applicable).
	// This is passthrough — Chat Completions has response_format.

	// Request stream_options.include_usage for streaming to get token counts.
	if result.Stream {
		result.StreamOptions = &StreamOptions{IncludeUsage: true}
	}

	return result, nil
}

// responsesInputToOpenAIMessages parses the Responses API input field
// and converts it to Chat Completions messages.
func responsesInputToOpenAIMessages(input json.RawMessage, instructions *string) ([]ChatMessage, error) {
	var messages []ChatMessage

	// Add system message from instructions.
	if instructions != nil && *instructions != "" {
		messages = append(messages, ChatMessage{
			Role:    "system",
			Content: *instructions,
		})
	}

	if len(input) == 0 {
		return messages, nil
	}

	// Try parsing as a string first.
	var textInput string
	if err := json.Unmarshal(input, &textInput); err == nil {
		messages = append(messages, ChatMessage{
			Role:    "user",
			Content: textInput,
		})
		return messages, nil
	}

	// Parse as array of InputItems.
	var items []InputItem
	if err := json.Unmarshal(input, &items); err != nil {
		return nil, fmt.Errorf("input must be a string or array of input items: %w", err)
	}

	for _, item := range items {
		switch item.Type {
		case "message":
			msg, err := responsesMessageToOpenAIMessage(item)
			if err != nil {
				return nil, err
			}
			messages = append(messages, msg...)

		case "input_text":
			messages = append(messages, ChatMessage{
				Role:    "user",
				Content: item.Text,
			})

		case "input_image":
			// Map to user message with image_url content part.
			messages = append(messages, ChatMessage{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{
						"type":      "image_url",
						"image_url": map[string]string{"url": item.ImageURL},
					},
				},
			})

		case "input_file":
			return nil, fmt.Errorf("input_file content type is not supported by Chat Completions upstream")

		case "input_audio":
			// Attempt passthrough for CC providers that support audio.
			messages = append(messages, ChatMessage{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{
						"type": "input_audio",
						"input_audio": map[string]string{
							"data":   item.Data,
							"format": item.Format,
						},
					},
				},
			})

		case "function_call":
			// Map to assistant message with tool_calls.
			messages = append(messages, ChatMessage{
				Role: "assistant",
				ToolCalls: []ToolCall{{
					ID:   item.CallID,
					Type: "function",
					Function: FunctionCall{
						Name:      item.Name,
						Arguments: item.Arguments,
					},
				}},
			})

		case "function_call_output":
			// Map to tool role message.
			messages = append(messages, ChatMessage{
				Role:       "tool",
				ToolCallID: item.CallID,
				Content:    item.Output,
			})

		case "item_reference":
			// Responses-only feature; silently drop.

		default:
			return nil, fmt.Errorf("unsupported input item type: %s", item.Type)
		}
	}

	return messages, nil
}

// responsesMessageToOpenAIMessage converts a Responses API message input item
// to one or more Chat Completions messages.
func responsesMessageToOpenAIMessage(item InputItem) ([]ChatMessage, error) {
	role := item.Role
	if role == "" {
		role = "user"
	}

	if len(item.Content) == 0 {
		return []ChatMessage{{Role: role, Content: ""}}, nil
	}

	// Try parsing content as array of InputItems.
	var contentItems []InputItem
	if err := json.Unmarshal(item.Content, &contentItems); err != nil {
		// Try as string.
		var text string
		if err := json.Unmarshal(item.Content, &text); err == nil {
			return []ChatMessage{{Role: role, Content: text}}, nil
		}
		return nil, fmt.Errorf("invalid message content")
	}

	// Build content parts.
	var parts []interface{}
	for _, ci := range contentItems {
		switch ci.Type {
		case "input_text":
			parts = append(parts, map[string]interface{}{
				"type": "text",
				"text": ci.Text,
			})
		case "input_image":
			parts = append(parts, map[string]interface{}{
				"type":      "image_url",
				"image_url": map[string]string{"url": ci.ImageURL},
			})
		case "input_file":
			return nil, fmt.Errorf("input_file content type is not supported by Chat Completions upstream")
		default:
			parts = append(parts, map[string]interface{}{
				"type": "text",
				"text": ci.Text,
			})
		}
	}

	if len(parts) == 1 {
		// Single text part: use string content.
		if m, ok := parts[0].(map[string]interface{}); ok {
			if m["type"] == "text" {
				return []ChatMessage{{Role: role, Content: m["text"]}}, nil
			}
		}
	}

	return []ChatMessage{{Role: role, Content: parts}}, nil
}

// responsesToolChoiceToOpenAI converts Responses API tool_choice to Chat Completions format.
func responsesToolChoiceToOpenAI(tc interface{}) (interface{}, error) {
	switch v := tc.(type) {
	case string:
		switch v {
		case "none", "auto", "required":
			return v, nil
		default:
			return nil, fmt.Errorf("unsupported tool_choice value: %s", v)
		}
	case map[string]interface{}:
		if name, ok := v["name"].(string); ok {
			return map[string]interface{}{
				"type": "function",
				"function": map[string]string{
					"name": name,
				},
			}, nil
		}
		return "auto", nil
	default:
		return nil, fmt.Errorf("unsupported tool_choice type")
	}
}
