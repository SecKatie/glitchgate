// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"fmt"
	"strings"
	"time"

	"github.com/seckatie/glitchgate/internal/provider/openai"
)

// canonicalStopReasonToOpenAIFinish maps canonical stop reason to OpenAI finish_reason.
func canonicalStopReasonToOpenAIFinish(sr StopReason) *string {
	switch sr {
	case StopReasonUnset:
		return nil
	case StopReasonMaxTokens:
		s := "length"
		return &s
	case StopReasonToolUse:
		s := "tool_calls"
		return &s
	case StopReasonContentFilter:
		s := "content_filter"
		return &s
	default:
		s := "stop"
		return &s
	}
}

// openAIFinishToCanonical maps OpenAI finish_reason to canonical.
func openAIFinishToCanonical(finishReason *string) StopReason {
	if finishReason == nil {
		return StopReasonUnset
	}
	switch *finishReason {
	case "length":
		return StopReasonMaxTokens
	case "tool_calls":
		return StopReasonToolUse
	case "content_filter":
		return StopReasonContentFilter
	default:
		return StopReasonEndTurn
	}
}

// OpenAIRequestToCanonical converts a openai.ChatCompletionRequest to canonical form.
func OpenAIRequestToCanonical(req *openai.ChatCompletionRequest) (*CanonicalRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request must not be nil")
	}

	canon := &CanonicalRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
	}

	if req.MaxTokens != nil {
		canon.MaxTokens = req.MaxTokens
	}

	// Parse stop sequences.
	if req.Stop != nil {
		seqs, err := parseStopSequences(req.Stop)
		if err != nil {
			return nil, fmt.Errorf("invalid stop field: %w", err)
		}
		canon.StopSequences = seqs
	}

	// Extract system messages and convert other messages.
	var systemParts []string
	for _, msg := range req.Messages {
		switch msg.Role {
		case "system":
			text, err := extractTextContent(msg.Content)
			if err != nil {
				return nil, fmt.Errorf("invalid system message content: %w", err)
			}
			systemParts = append(systemParts, text)

		case "user":
			canonMsg, err := openAIUserMessageToCanonical(msg)
			if err != nil {
				return nil, fmt.Errorf("invalid user message: %w", err)
			}
			canon.Messages = append(canon.Messages, canonMsg)

		case "assistant":
			canonMsg := openAIAssistantMessageToCanonical(msg)
			canon.Messages = append(canon.Messages, canonMsg)

		case "tool":
			text, err := extractTextContent(msg.Content)
			if err != nil {
				return nil, fmt.Errorf("invalid tool message content: %w", err)
			}
			canon.Messages = append(canon.Messages, CanonicalMessage{
				Role: "user",
				Blocks: []CanonicalBlock{{
					Type:           BlockToolResult,
					ToolUseID:      msg.ToolCallID,
					ToolResultText: text,
				}},
			})

		default:
			return nil, fmt.Errorf("unsupported message role: %s", msg.Role)
		}
	}

	if len(systemParts) > 0 {
		canon.System = strings.Join(systemParts, "\n")
	}

	// Convert tools.
	for _, t := range req.Tools {
		if t.Type != "function" {
			continue
		}
		canon.Tools = append(canon.Tools, CanonicalTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}

	// Convert tool_choice.
	if req.ToolChoice != nil {
		tc, err := openAIToolChoiceToCanonical(req.ToolChoice)
		if err != nil {
			return nil, fmt.Errorf("invalid tool_choice: %w", err)
		}
		canon.ToolChoice = tc
	}

	// Convert reasoning_effort.
	if req.ReasoningEffort != nil && *req.ReasoningEffort != "" {
		canon.Thinking = &CanonicalThinking{
			Enabled: true,
			Effort:  *req.ReasoningEffort,
		}
	}

	return canon, nil
}

func openAIUserMessageToCanonical(msg openai.ChatMessage) (CanonicalMessage, error) {
	canonMsg := CanonicalMessage{Role: "user"}

	switch v := msg.Content.(type) {
	case string:
		canonMsg.Blocks = []CanonicalBlock{{Type: BlockText, Text: v}}
	case []any:
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			t, _ := m["type"].(string)
			switch t {
			case "text":
				if text, ok := m["text"].(string); ok {
					canonMsg.Blocks = append(canonMsg.Blocks, CanonicalBlock{Type: BlockText, Text: text})
				}
			case "image_url":
				imgURL, ok := m["image_url"].(map[string]any)
				if !ok {
					continue
				}
				url, _ := imgURL["url"].(string)
				if url == "" {
					continue
				}
				cb := CanonicalBlock{Type: BlockImage}
				if strings.HasPrefix(url, "data:") {
					parts := splitDataURI(url)
					if parts != nil {
						cb.ImageMediaType = parts[0]
						cb.ImageData = parts[1]
					}
				} else {
					cb.ImageURL = url
				}
				canonMsg.Blocks = append(canonMsg.Blocks, cb)
			case "file":
				fileObj, ok := m["file"].(map[string]any)
				if !ok {
					continue
				}
				fileData, _ := fileObj["file_data"].(string)
				if fileData == "" {
					continue
				}
				cb := CanonicalBlock{Type: BlockDocument}
				if fn, ok := fileObj["filename"].(string); ok {
					cb.DocFilename = fn
				}
				// Parse data URI: "data:application/pdf;base64,<data>"
				if strings.HasPrefix(fileData, "data:") {
					parts := splitDataURI(fileData)
					if parts != nil {
						cb.DocMediaType = parts[0]
						cb.DocData = parts[1]
					}
				}
				canonMsg.Blocks = append(canonMsg.Blocks, cb)
			}
		}
	case nil:
		canonMsg.Blocks = []CanonicalBlock{{Type: BlockText, Text: ""}}
	default:
		text, err := extractTextContent(msg.Content)
		if err != nil {
			return canonMsg, err
		}
		canonMsg.Blocks = []CanonicalBlock{{Type: BlockText, Text: text}}
	}

	return canonMsg, nil
}

func openAIAssistantMessageToCanonical(msg openai.ChatMessage) CanonicalMessage {
	canonMsg := CanonicalMessage{Role: "assistant"}

	// Reasoning/thinking content.
	reasoning := msg.Reasoning
	if reasoning == "" {
		reasoning = msg.ReasoningContent
	}
	if reasoning != "" {
		canonMsg.Blocks = append(canonMsg.Blocks, CanonicalBlock{
			Type:         BlockThinking,
			ThinkingText: reasoning,
		})
	}

	// Text content.
	text, _ := extractTextContent(msg.Content)
	if text != "" {
		canonMsg.Blocks = append(canonMsg.Blocks, CanonicalBlock{Type: BlockText, Text: text})
	}

	// Refusal.
	if msg.Refusal != "" {
		canonMsg.Blocks = append(canonMsg.Blocks, CanonicalBlock{
			Type:        BlockRefusal,
			RefusalText: msg.Refusal,
		})
	}

	// Tool calls.
	for _, tc := range msg.ToolCalls {
		canonMsg.Blocks = append(canonMsg.Blocks, CanonicalBlock{
			Type:      BlockToolUse,
			ToolID:    tc.ID,
			ToolName:  tc.Function.Name,
			ToolInput: unmarshalToolArgs(tc.Function.Arguments),
		})
	}

	return canonMsg
}

func openAIToolChoiceToCanonical(tc any) (*CanonicalToolChoice, error) {
	switch v := tc.(type) {
	case string:
		switch v {
		case "none":
			return &CanonicalToolChoice{Mode: ToolChoiceNone}, nil
		case "auto":
			return &CanonicalToolChoice{Mode: ToolChoiceAuto}, nil
		case "required":
			return &CanonicalToolChoice{Mode: ToolChoiceRequired}, nil
		default:
			return nil, fmt.Errorf("unknown tool_choice string: %s", v)
		}
	case map[string]any:
		if fn, ok := v["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok {
				return &CanonicalToolChoice{Mode: ToolChoiceSpecific, FunctionName: name}, nil
			}
		}
		return &CanonicalToolChoice{Mode: ToolChoiceAuto}, nil
	default:
		return nil, fmt.Errorf("unsupported tool_choice type")
	}
}

// CanonicalToOpenAIRequest converts a canonical request to OpenAI Chat Completions format.
func CanonicalToOpenAIRequest(canon *CanonicalRequest) (*openai.ChatCompletionRequest, error) {
	if canon == nil {
		return nil, fmt.Errorf("canonical request must not be nil")
	}

	result := &openai.ChatCompletionRequest{
		Model:       canon.Model,
		Temperature: canon.Temperature,
		TopP:        canon.TopP,
		Stream:      canon.Stream,
	}

	if canon.MaxTokens != nil {
		result.MaxTokens = canon.MaxTokens
	}

	if len(canon.StopSequences) > 0 {
		result.Stop = canon.StopSequences
	}

	var messages []openai.ChatMessage

	// System message.
	if canon.System != "" {
		messages = append(messages, openai.ChatMessage{
			Role:    "system",
			Content: canon.System,
		})
	}

	// Convert messages.
	for _, msg := range canon.Messages {
		oaiMsgs := canonicalMessageToOpenAI(msg)
		messages = append(messages, oaiMsgs...)
	}

	result.Messages = messages

	// Convert tools.
	for _, t := range canon.Tools {
		result.Tools = append(result.Tools, openai.Tool{
			Type: "function",
			Function: openai.ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	// Convert tool_choice.
	if canon.ToolChoice != nil {
		result.ToolChoice = canonicalToolChoiceToOpenAI(canon.ToolChoice)
	}

	// Convert thinking to reasoning_effort.
	if canon.Thinking != nil && canon.Thinking.Enabled {
		effort := canon.Thinking.Effort
		if effort == "" && canon.Thinking.BudgetTokens > 0 {
			effort = budgetTokensToEffort(canon.Thinking.BudgetTokens)
		}
		if effort != "" {
			result.ReasoningEffort = &effort
		}
	}

	// Request stream_options.include_usage for streaming.
	if result.Stream {
		result.StreamOptions = &openai.StreamOptions{IncludeUsage: true}
	}

	return result, nil
}

// canonicalMessageToOpenAI converts a canonical message to one or more OpenAI messages.
// Tool result blocks become separate "tool" role messages.
func canonicalMessageToOpenAI(msg CanonicalMessage) []openai.ChatMessage {
	if msg.Role == "user" {
		return canonicalUserToOpenAI(msg)
	}

	// Assistant message.
	oaiMsg := openai.ChatMessage{Role: "assistant"}
	var textParts []string

	for _, b := range msg.Blocks {
		switch b.Type {
		case BlockText:
			textParts = append(textParts, b.Text)
		case BlockThinking:
			oaiMsg.ReasoningContent = b.ThinkingText
		case BlockRefusal:
			oaiMsg.Refusal = b.RefusalText
		case BlockToolUse:
			oaiMsg.ToolCalls = append(oaiMsg.ToolCalls, openai.ToolCall{
				ID:   b.ToolID,
				Type: "function",
				Function: openai.FunctionCall{
					Name:      b.ToolName,
					Arguments: string(marshalToolInput(b.ToolInput)),
				},
			})
		}
	}

	if len(textParts) > 0 {
		oaiMsg.Content = strings.Join(textParts, "")
	}

	return []openai.ChatMessage{oaiMsg}
}

// canonicalUserToOpenAI handles user messages, splitting tool results into separate messages.
func canonicalUserToOpenAI(msg CanonicalMessage) []openai.ChatMessage {
	var messages []openai.ChatMessage
	var parts []openai.ContentPart
	hasImages := false

	flushParts := func() {
		if len(parts) == 0 {
			return
		}
		if hasImages {
			messages = append(messages, openai.ChatMessage{Role: "user", Content: parts})
		} else {
			var texts []string
			for _, p := range parts {
				texts = append(texts, p.Text)
			}
			messages = append(messages, openai.ChatMessage{Role: "user", Content: strings.Join(texts, "")})
		}
		parts = nil
		hasImages = false
	}

	for _, b := range msg.Blocks {
		switch b.Type {
		case BlockText:
			parts = append(parts, openai.ContentPart{Type: "text", Text: b.Text})
		case BlockImage:
			url := b.ImageURL
			if b.ImageData != "" {
				url = buildDataURI(b.ImageMediaType, b.ImageData)
			}
			if url != "" {
				parts = append(parts, openai.ContentPart{
					Type:     "image_url",
					ImageURL: &openai.ImageURLContent{URL: url},
				})
				hasImages = true
			}
		case BlockAudio:
			parts = append(parts, openai.ContentPart{
				Type: "input_audio",
				InputAudio: &openai.InputAudioContent{
					Data:   b.AudioData,
					Format: b.AudioFormat,
				},
			})
			hasImages = true // force multipart content
		case BlockDocument:
			fileData := b.DocURL
			if b.DocData != "" {
				fileData = buildDataURI(b.DocMediaType, b.DocData)
			}
			if fileData != "" {
				parts = append(parts, openai.ContentPart{
					Type: "file",
					File: &openai.FileContent{
						FileData: fileData,
						Filename: b.DocFilename,
					},
				})
				hasImages = true // force multipart content
			}
		case BlockToolResult:
			flushParts()
			messages = append(messages, openai.ChatMessage{
				Role:       "tool",
				ToolCallID: b.ToolUseID,
				Content:    b.ToolResultText,
			})
		}
	}

	flushParts()
	return messages
}

func canonicalToolChoiceToOpenAI(tc *CanonicalToolChoice) any {
	switch tc.Mode {
	case ToolChoiceNone:
		return "none"
	case ToolChoiceRequired:
		return "required"
	case ToolChoiceSpecific:
		return map[string]any{
			"type": "function",
			"function": map[string]string{
				"name": tc.FunctionName,
			},
		}
	default:
		return "auto"
	}
}

// OpenAIResponseToCanonical converts a openai.ChatCompletionResponse to canonical form.
func OpenAIResponseToCanonical(resp *openai.ChatCompletionResponse) *CanonicalResponse {
	canon := &CanonicalResponse{
		ID:    strings.TrimPrefix(resp.ID, "chatcmpl-"),
		Model: resp.Model,
	}

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		canon.StopReason = openAIFinishToCanonical(choice.FinishReason)

		if msg := choice.Message; msg != nil {
			// Reasoning/thinking.
			reasoning := msg.Reasoning
			if reasoning == "" {
				reasoning = msg.ReasoningContent
			}
			if reasoning != "" {
				canon.Content = append(canon.Content, CanonicalBlock{
					Type:         BlockThinking,
					ThinkingText: reasoning,
				})
			}

			// Text content.
			switch c := msg.Content.(type) {
			case string:
				if c != "" {
					canon.Content = append(canon.Content, CanonicalBlock{Type: BlockText, Text: c})
				}
			case []any:
				for _, part := range c {
					if m, ok := part.(map[string]any); ok {
						if t, ok := m["text"].(string); ok && t != "" {
							canon.Content = append(canon.Content, CanonicalBlock{Type: BlockText, Text: t})
						}
					}
				}
			}

			// Refusal.
			if msg.Refusal != "" {
				canon.Content = append(canon.Content, CanonicalBlock{
					Type:        BlockRefusal,
					RefusalText: msg.Refusal,
				})
			}

			// Tool calls.
			for _, tc := range msg.ToolCalls {
				canon.Content = append(canon.Content, CanonicalBlock{
					Type:      BlockToolUse,
					ToolID:    tc.ID,
					ToolName:  tc.Function.Name,
					ToolInput: unmarshalToolArgs(tc.Function.Arguments),
				})
			}
		}
	}

	// Usage.
	if resp.Usage != nil {
		canon.Usage = CanonicalUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		}
		if resp.Usage.PromptTokensDetails != nil {
			canon.Usage.CacheReadInputTokens = resp.Usage.PromptTokensDetails.CachedTokens
		}
		if resp.Usage.CompletionTokensDetails != nil {
			canon.Usage.ReasoningTokens = resp.Usage.CompletionTokensDetails.ReasoningTokens
		}
	}

	return canon
}

// CanonicalToOpenAIResponse converts a canonical response to OpenAI format.
func CanonicalToOpenAIResponse(canon *CanonicalResponse, model string) *openai.ChatCompletionResponse {
	msg := &openai.ChatMessage{Role: "assistant"}

	var textParts []string
	var thinkingParts []string
	var toolCalls []openai.ToolCall

	for _, b := range canon.Content {
		switch b.Type {
		case BlockText:
			textParts = append(textParts, b.Text)
		case BlockThinking:
			thinkingParts = append(thinkingParts, b.ThinkingText)
		case BlockRefusal:
			msg.Refusal = b.RefusalText
		case BlockToolUse:
			toolCalls = append(toolCalls, openai.ToolCall{
				ID:   b.ToolID,
				Type: "function",
				Function: openai.FunctionCall{
					Name:      b.ToolName,
					Arguments: string(marshalToolInput(b.ToolInput)),
				},
			})
		}
	}

	if len(textParts) > 0 {
		msg.Content = strings.Join(textParts, "")
	}
	if len(thinkingParts) > 0 {
		msg.ReasoningContent = strings.Join(thinkingParts, "\n\n")
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}

	finishReason := canonicalStopReasonToOpenAIFinish(canon.StopReason)
	if canon.StopReason == StopReasonUnknown && canon.RawStopReason != nil {
		finishReason = canon.RawStopReason
	}

	result := &openai.ChatCompletionResponse{
		ID:      "chatcmpl-" + canon.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []openai.Choice{{
			Index:        0,
			Message:      msg,
			FinishReason: finishReason,
		}},
	}

	// Usage.
	// In canonical form, InputTokens may have CacheReadInputTokens subtracted out
	// (Anthropic convention). OpenAI PromptTokens includes cached tokens.
	promptTokens := canon.Usage.InputTokens + canon.Usage.CacheReadInputTokens
	result.Usage = &openai.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: canon.Usage.OutputTokens,
		TotalTokens:      promptTokens + canon.Usage.OutputTokens,
	}
	if canon.Usage.CacheReadInputTokens > 0 {
		result.Usage.PromptTokensDetails = &openai.PromptTokensDetails{
			CachedTokens: canon.Usage.CacheReadInputTokens,
		}
	}
	if canon.Usage.ReasoningTokens > 0 {
		result.Usage.CompletionTokensDetails = &openai.CompletionTokensDetails{
			ReasoningTokens: canon.Usage.ReasoningTokens,
		}
	}

	return result
}
