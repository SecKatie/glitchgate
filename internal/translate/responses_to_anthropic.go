// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"fmt"

	"codeberg.org/kglitchy/glitchgate/internal/provider/anthropic"
)

// ResponsesToAnthropic translates a Responses API request to an Anthropic MessagesRequest.
func ResponsesToAnthropic(req *ResponsesRequest, upstreamModel string) (*anthropic.MessagesRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request must not be nil")
	}

	result := &anthropic.MessagesRequest{
		Model:       upstreamModel,
		Stream:      req.Stream != nil && *req.Stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
	}

	// Set max_tokens.
	if req.MaxOutputTokens != nil {
		result.MaxTokens = *req.MaxOutputTokens
	} else {
		result.MaxTokens = defaultMaxTokens
	}

	// Map instructions to system.
	if req.Instructions != nil && *req.Instructions != "" {
		result.System = *req.Instructions
	}

	// Parse input and convert to Anthropic messages.
	messages, err := responsesInputToAnthropicMessages(req.Input)
	if err != nil {
		return nil, fmt.Errorf("translating input: %w", err)
	}
	result.Messages = messages

	// Translate tools.
	for _, t := range req.Tools {
		if t.Type != "function" {
			// Built-in tools (web_search, etc.) can't be translated to Anthropic.
			return nil, fmt.Errorf("tool type %q is not supported for Anthropic upstream; only function tools are translatable", t.Type)
		}
		var schema interface{}
		if len(t.Parameters) > 0 {
			if err := json.Unmarshal(t.Parameters, &schema); err != nil {
				schema = map[string]interface{}{}
			}
		}
		result.Tools = append(result.Tools, anthropic.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}

	// Translate tool_choice.
	if req.ToolChoice != nil {
		tc, err := responsesToolChoiceToAnthropic(req.ToolChoice)
		if err != nil {
			return nil, err
		}
		result.ToolChoice = tc
	}

	// Translate reasoning effort to Anthropic thinking config.
	if req.Reasoning != nil && req.Reasoning.Effort != nil && *req.Reasoning.Effort != "" {
		budget := effortToBudgetTokens(*req.Reasoning.Effort, result.MaxTokens)
		result.Thinking = &anthropic.ThinkingConfig{
			Type:         "enabled",
			BudgetTokens: budget,
		}
	}

	return result, nil
}

// responsesInputToAnthropicMessages parses the Responses API input field
// and converts it to Anthropic messages.
func responsesInputToAnthropicMessages(input json.RawMessage) ([]anthropic.Message, error) {
	if len(input) == 0 {
		return nil, nil
	}

	// Try parsing as a string first.
	var textInput string
	if err := json.Unmarshal(input, &textInput); err == nil {
		return []anthropic.Message{{
			Role:    "user",
			Content: textInput,
		}}, nil
	}

	// Parse as array of InputItems.
	var items []InputItem
	if err := json.Unmarshal(input, &items); err != nil {
		return nil, fmt.Errorf("input must be a string or array of input items: %w", err)
	}

	var messages []anthropic.Message

	for _, item := range items {
		switch item.Type {
		case "message":
			msg, err := responsesMessageToAnthropicMessage(item)
			if err != nil {
				return nil, err
			}
			messages = append(messages, msg)

		case "input_text":
			messages = append(messages, anthropic.Message{
				Role:    "user",
				Content: item.Text,
			})

		case "input_image":
			block, err := responsesImageToAnthropicBlock(item)
			if err != nil {
				return nil, err
			}
			messages = append(messages, anthropic.Message{
				Role:    "user",
				Content: []anthropic.ContentBlock{block},
			})

		case "input_file":
			return nil, fmt.Errorf("input_file content type is not supported by Anthropic upstream")

		case "input_audio":
			return nil, fmt.Errorf("input_audio content type is not supported by Anthropic upstream")

		case "function_call":
			// Map to assistant message with tool_use block.
			var inputData interface{}
			if item.Arguments != "" {
				if err := json.Unmarshal([]byte(item.Arguments), &inputData); err != nil {
					inputData = map[string]interface{}{}
				}
			}
			messages = append(messages, anthropic.Message{
				Role: "assistant",
				Content: []anthropic.ContentBlock{{
					Type:  "tool_use",
					ID:    item.CallID,
					Name:  item.Name,
					Input: inputData,
				}},
			})

		case "function_call_output":
			// Map to user message with tool_result block.
			messages = append(messages, anthropic.Message{
				Role: "user",
				Content: []anthropic.ContentBlock{{
					Type: "tool_result",
					ID:   item.CallID,
					Text: item.Output,
				}},
			})

		case "item_reference":
			// Responses-only feature; silently drop.

		default:
			return nil, fmt.Errorf("unsupported input item type: %s", item.Type)
		}
	}

	return messages, nil
}

// responsesMessageToAnthropicMessage converts a Responses API message input item to an Anthropic message.
func responsesMessageToAnthropicMessage(item InputItem) (anthropic.Message, error) {
	role := item.Role
	if role == "" {
		role = "user"
	}

	// Parse the content array.
	if len(item.Content) == 0 {
		return anthropic.Message{Role: role, Content: ""}, nil
	}

	var contentItems []InputItem
	if err := json.Unmarshal(item.Content, &contentItems); err != nil {
		// Try as string.
		var text string
		if err := json.Unmarshal(item.Content, &text); err == nil {
			return anthropic.Message{Role: role, Content: text}, nil
		}
		return anthropic.Message{}, fmt.Errorf("invalid message content: %w", err)
	}

	var blocks []anthropic.ContentBlock
	for _, ci := range contentItems {
		switch ci.Type {
		case "input_text":
			blocks = append(blocks, anthropic.ContentBlock{
				Type: "text",
				Text: ci.Text,
			})
		case "input_image":
			block, err := responsesImageToAnthropicBlock(ci)
			if err != nil {
				return anthropic.Message{}, err
			}
			blocks = append(blocks, block)
		case "input_file":
			return anthropic.Message{}, fmt.Errorf("input_file content type is not supported by Anthropic upstream")
		case "input_audio":
			return anthropic.Message{}, fmt.Errorf("input_audio content type is not supported by Anthropic upstream")
		default:
			blocks = append(blocks, anthropic.ContentBlock{
				Type: "text",
				Text: ci.Text,
			})
		}
	}

	return anthropic.Message{Role: role, Content: blocks}, nil
}

// responsesImageToAnthropicBlock converts a Responses API image input to an Anthropic image block.
func responsesImageToAnthropicBlock(item InputItem) (anthropic.ContentBlock, error) {
	if item.ImageURL == "" {
		return anthropic.ContentBlock{}, fmt.Errorf("input_image requires image_url")
	}
	return imageURLToAnthropicBlock(item.ImageURL)
}

// responsesToolChoiceToAnthropic converts Responses API tool_choice to Anthropic format.
func responsesToolChoiceToAnthropic(tc interface{}) (interface{}, error) {
	switch v := tc.(type) {
	case string:
		switch v {
		case "none":
			return nil, nil
		case "auto":
			return map[string]string{"type": "auto"}, nil
		case "required":
			return map[string]string{"type": "any"}, nil
		default:
			return nil, fmt.Errorf("unsupported tool_choice value: %s", v)
		}
	case map[string]interface{}:
		if name, ok := v["name"].(string); ok {
			return map[string]string{"type": "tool", "name": name}, nil
		}
		return map[string]string{"type": "auto"}, nil
	default:
		return nil, fmt.Errorf("unsupported tool_choice type")
	}
}
