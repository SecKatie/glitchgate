// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/kglitchy/llm-proxy/internal/provider/anthropic"
)

// AnthropicToOpenAIRequest translates an Anthropic MessagesRequest into an
// OpenAI ChatCompletionRequest. This is the reverse of OpenAIToAnthropic and
// is used when an Anthropic-format client sends a request to an OpenAI-native
// provider (e.g. GitHub Copilot).
func AnthropicToOpenAIRequest(req *anthropic.MessagesRequest) (*ChatCompletionRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request must not be nil")
	}

	result := &ChatCompletionRequest{
		Model:       req.Model,
		Stream:      req.Stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
	}

	if req.MaxTokens > 0 {
		mt := req.MaxTokens
		result.MaxTokens = &mt
	}

	if len(req.StopSequences) > 0 {
		result.Stop = req.StopSequences
	}

	var messages []ChatMessage

	// Convert Anthropic system field to an OpenAI system message.
	if req.System != nil {
		systemText := extractAnthropicSystem(req.System)
		if systemText != "" {
			messages = append(messages, ChatMessage{
				Role:    "system",
				Content: systemText,
			})
		}
	}

	// Convert Anthropic messages to OpenAI messages.
	for _, msg := range req.Messages {
		oaiMsgs, err := translateAnthropicMessage(msg)
		if err != nil {
			return nil, fmt.Errorf("translating %s message: %w", msg.Role, err)
		}
		messages = append(messages, oaiMsgs...)
	}

	result.Messages = messages

	// Convert Anthropic tools to OpenAI format.
	for _, t := range req.Tools {
		result.Tools = append(result.Tools, OpenAITool{
			Type: "function",
			Function: ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	// Convert Anthropic tool_choice to OpenAI format.
	if req.ToolChoice != nil {
		result.ToolChoice = translateAnthropicToolChoice(req.ToolChoice)
	}

	return result, nil
}

// extractAnthropicSystem extracts a plain text string from the Anthropic system
// field, which can be a string or an array of system blocks.
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

// translateAnthropicMessage converts an Anthropic Message to one or more
// OpenAI ChatMessages. Anthropic tool_result blocks become separate tool
// messages in OpenAI format.
func translateAnthropicMessage(msg anthropic.Message) ([]ChatMessage, error) {
	// Handle string content (simple text message).
	if text, ok := msg.Content.(string); ok {
		return []ChatMessage{{
			Role:    msg.Role,
			Content: text,
		}}, nil
	}

	// Handle array content (content blocks).
	blocks, err := parseContentBlocks(msg.Content)
	if err != nil {
		return nil, err
	}

	if msg.Role == "user" {
		return translateAnthropicUserBlocks(blocks)
	}

	// Assistant message: may contain text and tool_use blocks.
	return translateAnthropicAssistantBlocks(blocks)
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

// translateAnthropicUserBlocks converts user content blocks. Text blocks are
// combined into a single user message. Tool_result blocks become separate
// OpenAI tool messages.
func translateAnthropicUserBlocks(blocks []anthropic.ContentBlock) ([]ChatMessage, error) {
	var messages []ChatMessage
	var textParts []string

	for _, b := range blocks {
		switch b.Type {
		case "text":
			textParts = append(textParts, b.Text)
		case "tool_result":
			// Flush any accumulated text first.
			if len(textParts) > 0 {
				messages = append(messages, ChatMessage{
					Role:    "user",
					Content: strings.Join(textParts, ""),
				})
				textParts = nil
			}
			messages = append(messages, ChatMessage{
				Role:       "tool",
				ToolCallID: b.ID,
				Content:    b.Text,
			})
		}
	}

	if len(textParts) > 0 {
		messages = append(messages, ChatMessage{
			Role:    "user",
			Content: strings.Join(textParts, ""),
		})
	}

	return messages, nil
}

// translateAnthropicAssistantBlocks converts assistant content blocks. Text
// blocks are combined into the message content, and tool_use blocks become
// OpenAI tool_calls.
func translateAnthropicAssistantBlocks(blocks []anthropic.ContentBlock) ([]ChatMessage, error) {
	msg := ChatMessage{Role: "assistant"}
	var textParts []string

	for _, b := range blocks {
		switch b.Type {
		case "text":
			textParts = append(textParts, b.Text)
		case "tool_use":
			args, err := json.Marshal(b.Input)
			if err != nil {
				args = []byte("{}")
			}
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				ID:   b.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      b.Name,
					Arguments: string(args),
				},
			})
		}
	}

	if len(textParts) > 0 {
		msg.Content = strings.Join(textParts, "")
	}

	return []ChatMessage{msg}, nil
}

// translateAnthropicToolChoice converts Anthropic tool_choice to OpenAI format.
// Anthropic: {"type":"auto"}, {"type":"any"}, {"type":"tool","name":"..."}
// OpenAI: "auto", "required", {"type":"function","function":{"name":"..."}}
func translateAnthropicToolChoice(tc interface{}) interface{} {
	m, ok := tc.(map[string]interface{})
	if !ok {
		return "auto"
	}

	t, _ := m["type"].(string)
	switch t {
	case "auto":
		return "auto"
	case "any":
		return "required"
	case "tool":
		name, _ := m["name"].(string)
		return map[string]interface{}{
			"type": "function",
			"function": map[string]string{
				"name": name,
			},
		}
	default:
		return "auto"
	}
}
