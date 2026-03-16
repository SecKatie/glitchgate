package translate

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
)

const defaultMaxTokens = 4096

// OpenAIToAnthropic translates an OpenAI ChatCompletionRequest into an
// Anthropic MessagesRequest. System messages are extracted from the
// messages array and placed into the Anthropic system field.
func OpenAIToAnthropic(req *ChatCompletionRequest) (*anthropic.MessagesRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request must not be nil")
	}

	result := &anthropic.MessagesRequest{
		Model:  req.Model,
		Stream: req.Stream,
	}

	// Set max_tokens, defaulting to 4096 if not provided.
	if req.MaxTokens != nil {
		result.MaxTokens = *req.MaxTokens
	} else {
		result.MaxTokens = defaultMaxTokens
	}

	// Copy optional parameters.
	result.Temperature = req.Temperature
	result.TopP = req.TopP

	// Translate stop sequences: handle string or []string.
	if req.Stop != nil {
		seqs, err := parseStopSequences(req.Stop)
		if err != nil {
			return nil, fmt.Errorf("invalid stop field: %w", err)
		}
		result.StopSequences = seqs
	}

	// Extract system messages and translate user/assistant messages.
	var systemParts []string
	var messages []anthropic.Message

	for _, msg := range req.Messages {
		switch msg.Role {
		case "system":
			text, err := extractTextContent(msg.Content)
			if err != nil {
				return nil, fmt.Errorf("invalid system message content: %w", err)
			}
			systemParts = append(systemParts, text)

		case "user", "assistant":
			anthMsg, err := translateMessage(msg)
			if err != nil {
				return nil, fmt.Errorf("invalid %s message: %w", msg.Role, err)
			}
			messages = append(messages, anthMsg)

		case "tool":
			// Tool result messages map to user messages with tool_result content blocks.
			toolResult := anthropic.ContentBlock{
				Type: "tool_result",
				ID:   msg.ToolCallID,
			}
			text, err := extractTextContent(msg.Content)
			if err != nil {
				return nil, fmt.Errorf("invalid tool message content: %w", err)
			}
			toolResult.Text = text
			messages = append(messages, anthropic.Message{
				Role:    "user",
				Content: []anthropic.ContentBlock{toolResult},
			})

		default:
			return nil, fmt.Errorf("unsupported message role: %s", msg.Role)
		}
	}

	if len(systemParts) > 0 {
		result.System = strings.Join(systemParts, "\n")
	}
	result.Messages = messages

	// Translate tools.
	if len(req.Tools) > 0 {
		tools, err := translateTools(req.Tools)
		if err != nil {
			return nil, fmt.Errorf("invalid tools: %w", err)
		}
		result.Tools = tools
	}

	// Translate tool_choice.
	if req.ToolChoice != nil {
		tc, err := translateToolChoice(req.ToolChoice)
		if err != nil {
			return nil, fmt.Errorf("invalid tool_choice: %w", err)
		}
		result.ToolChoice = tc
	}

	// Translate reasoning_effort to Anthropic thinking config.
	if req.ReasoningEffort != nil && *req.ReasoningEffort != "" {
		budget := effortToBudgetTokens(*req.ReasoningEffort, result.MaxTokens)
		result.Thinking = &anthropic.ThinkingConfig{
			Type:         "enabled",
			BudgetTokens: budget,
		}
	}

	return result, nil
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
// field, which may be a string or an array of ContentPart objects.
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

// imageURLToAnthropicBlock converts an image URL string (data URI or plain URL)
// to an Anthropic image ContentBlock. Used by both OpenAI→Anthropic and
// Responses→Anthropic translation paths.
func imageURLToAnthropicBlock(imageURL string) (anthropic.ContentBlock, error) {
	if strings.HasPrefix(imageURL, "data:") {
		parts := strings.SplitN(imageURL, ",", 2)
		if len(parts) != 2 {
			return anthropic.ContentBlock{}, fmt.Errorf("invalid data URI for image")
		}
		mediaInfo := strings.TrimPrefix(parts[0], "data:")
		mediaInfo = strings.TrimSuffix(mediaInfo, ";base64")
		return anthropic.ContentBlock{
			Type: "image",
			Source: &anthropic.ImageSource{
				Type:      "base64",
				MediaType: mediaInfo,
				Data:      parts[1],
			},
		}, nil
	}
	return anthropic.ContentBlock{
		Type: "image",
		Source: &anthropic.ImageSource{
			Type: "url",
			URL:  imageURL,
		},
	}, nil
}

// translateOpenAIContentParts converts a multipart content array from OpenAI
// format to Anthropic content. Returns []anthropic.ContentBlock if images are
// present, otherwise returns a plain string (preserving backward compat).
func translateOpenAIContentParts(parts []interface{}) (interface{}, error) {
	var blocks []anthropic.ContentBlock
	hasImages := false

	for _, item := range parts {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		t, _ := m["type"].(string)
		switch t {
		case "text":
			if text, ok := m["text"].(string); ok {
				blocks = append(blocks, anthropic.ContentBlock{Type: "text", Text: text})
			}
		case "image_url":
			imgURL, ok := m["image_url"].(map[string]interface{})
			if !ok {
				continue
			}
			url, _ := imgURL["url"].(string)
			if url == "" {
				continue
			}
			block, err := imageURLToAnthropicBlock(url)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, block)
			hasImages = true
		}
	}

	if hasImages {
		return blocks, nil
	}
	var texts []string
	for _, b := range blocks {
		texts = append(texts, b.Text)
	}
	return strings.Join(texts, ""), nil
}

// translateMessage converts an OpenAI ChatMessage to an Anthropic Message.
func translateMessage(msg ChatMessage) (anthropic.Message, error) {
	anthMsg := anthropic.Message{
		Role: msg.Role,
	}

	// If the message has tool_calls (assistant message calling tools),
	// translate them to Anthropic tool_use content blocks.
	if len(msg.ToolCalls) > 0 {
		var blocks []anthropic.ContentBlock

		// Include any text content first.
		text, _ := extractTextContent(msg.Content)
		if text != "" {
			blocks = append(blocks, anthropic.ContentBlock{
				Type: "text",
				Text: text,
			})
		}

		for _, tc := range msg.ToolCalls {
			var input interface{}
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
					input = map[string]interface{}{}
				}
			}
			blocks = append(blocks, anthropic.ContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: input,
			})
		}
		anthMsg.Content = blocks
		return anthMsg, nil
	}

	// For user messages with multipart content, check for images.
	if msg.Role == "user" {
		if parts, ok := msg.Content.([]interface{}); ok {
			content, err := translateOpenAIContentParts(parts)
			if err != nil {
				return anthMsg, err
			}
			anthMsg.Content = content
			return anthMsg, nil
		}
	}

	// For simple text content, pass through directly.
	text, err := extractTextContent(msg.Content)
	if err != nil {
		return anthMsg, err
	}
	anthMsg.Content = text

	return anthMsg, nil
}

// translateTools converts OpenAI tools to Anthropic tool format.
func translateTools(tools []OpenAITool) ([]anthropic.Tool, error) {
	var result []anthropic.Tool
	for _, t := range tools {
		if t.Type != "function" {
			continue
		}
		result = append(result, anthropic.Tool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}
	return result, nil
}

// translateToolChoice converts OpenAI tool_choice to Anthropic format.
// OpenAI accepts "none", "auto", "required", or {"type":"function","function":{"name":"..."}}.
// Anthropic accepts {"type":"auto"}, {"type":"any"}, or {"type":"tool","name":"..."}.
func translateToolChoice(tc interface{}) (interface{}, error) {
	switch v := tc.(type) {
	case string:
		switch v {
		case "none":
			// No direct Anthropic equivalent; omit tool_choice.
			return nil, nil
		case "auto":
			return map[string]string{"type": "auto"}, nil
		case "required":
			return map[string]string{"type": "any"}, nil
		default:
			return nil, fmt.Errorf("unknown tool_choice string: %s", v)
		}
	case map[string]interface{}:
		// Object form: {"type":"function","function":{"name":"..."}}
		if fn, ok := v["function"].(map[string]interface{}); ok {
			if name, ok := fn["name"].(string); ok {
				return map[string]string{"type": "tool", "name": name}, nil
			}
		}
		return map[string]string{"type": "auto"}, nil
	default:
		return nil, fmt.Errorf("unsupported tool_choice type")
	}
}
