// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
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

	// Translate Anthropic thinking config to OpenAI reasoning_effort.
	if req.Thinking != nil && req.Thinking.Type == "enabled" {
		effort := budgetTokensToEffort(req.Thinking.BudgetTokens)
		result.ReasoningEffort = &effort
	}

	// Request stream_options.include_usage for streaming to get token counts.
	if result.Stream {
		result.StreamOptions = &StreamOptions{IncludeUsage: true}
	}

	return result, nil
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

// anthropicSourceToImageURL converts an Anthropic image source to a URL string
// suitable for an OpenAI image_url content part.
func anthropicSourceToImageURL(src *anthropic.ImageSource) string {
	if src == nil {
		return ""
	}
	if src.Type == "base64" {
		return "data:" + src.MediaType + ";base64," + src.Data
	}
	return src.URL
}

// translateAnthropicUserBlocks converts user content blocks. Text and image
// blocks are accumulated into a single user message. Tool_result blocks become
// separate OpenAI tool messages.
func translateAnthropicUserBlocks(blocks []anthropic.ContentBlock) ([]ChatMessage, error) {
	var messages []ChatMessage
	var parts []ContentPart

	flushParts := func() {
		if len(parts) == 0 {
			return
		}
		hasImages := false
		for _, p := range parts {
			if p.Type == "image_url" {
				hasImages = true
				break
			}
		}
		var content interface{}
		if hasImages {
			content = parts
		} else {
			var texts []string
			for _, p := range parts {
				texts = append(texts, p.Text)
			}
			content = strings.Join(texts, "")
		}
		messages = append(messages, ChatMessage{
			Role:    "user",
			Content: content,
		})
		parts = nil
	}

	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, ContentPart{Type: "text", Text: b.Text})
		case "image":
			url := anthropicSourceToImageURL(b.Source)
			if url != "" {
				parts = append(parts, ContentPart{
					Type:     "image_url",
					ImageURL: &ImageURLContent{URL: url},
				})
			}
		case "tool_result":
			flushParts()
			messages = append(messages, ChatMessage{
				Role:       "tool",
				ToolCallID: b.ToolUseID,
				Content:    extractToolResultText(b),
			})
		}
	}

	flushParts()
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
