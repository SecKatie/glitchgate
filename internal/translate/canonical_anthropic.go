// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"fmt"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
)

func canonicalStopReasonToAnthropic(sr StopReason) string {
	switch sr {
	case StopReasonMaxTokens:
		return "max_tokens"
	case StopReasonToolUse:
		return "tool_use"
	case StopReasonStopSequence:
		return "stop_sequence"
	default:
		return "end_turn"
	}
}

func anthropicStopReasonToCanonical(stopReason *string) (StopReason, *string) {
	if stopReason == nil {
		return StopReasonUnset, nil
	}
	switch *stopReason {
	case "max_tokens":
		return StopReasonMaxTokens, nil
	case "tool_use":
		return StopReasonToolUse, nil
	case "stop_sequence":
		return StopReasonStopSequence, nil
	case "end_turn":
		return StopReasonEndTurn, nil
	default:
		return StopReasonUnknown, stopReason
	}
}

// AnthropicRequestToCanonical converts an Anthropic MessagesRequest to the
// canonical intermediate representation.
func AnthropicRequestToCanonical(req *anthropic.MessagesRequest) (*CanonicalRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request must not be nil")
	}

	canon := &CanonicalRequest{
		Model:         req.Model,
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		TopK:          req.TopK,
		StopSequences: req.StopSequences,
		Stream:        req.Stream,
	}

	if req.MaxTokens > 0 {
		mt := req.MaxTokens
		canon.MaxTokens = &mt
	}

	// Extract system text.
	if req.System != nil {
		canon.System = extractAnthropicSystem(req.System)
	}

	// Convert messages.
	for _, msg := range req.Messages {
		canonMsg, err := anthropicMessageToCanonical(msg)
		if err != nil {
			return nil, fmt.Errorf("translating %s message: %w", msg.Role, err)
		}
		canon.Messages = append(canon.Messages, canonMsg)
	}

	// Convert tools.
	for _, t := range req.Tools {
		canon.Tools = append(canon.Tools, CanonicalTool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.InputSchema,
		})
	}

	// Convert tool_choice.
	if req.ToolChoice != nil {
		tc, err := anthropicToolChoiceToCanonical(req.ToolChoice)
		if err != nil {
			return nil, err
		}
		canon.ToolChoice = tc
	}

	// Convert thinking config.
	if req.Thinking != nil && req.Thinking.Type == "enabled" {
		canon.Thinking = &CanonicalThinking{
			Enabled:      true,
			BudgetTokens: req.Thinking.BudgetTokens,
			Effort:       budgetTokensToEffort(req.Thinking.BudgetTokens),
		}
	}

	return canon, nil
}

// anthropicMessageToCanonical converts a single Anthropic message.
func anthropicMessageToCanonical(msg anthropic.Message) (CanonicalMessage, error) {
	canonMsg := CanonicalMessage{Role: msg.Role}

	// String content → single text block.
	if text, ok := msg.Content.(string); ok {
		canonMsg.Blocks = []CanonicalBlock{{Type: BlockText, Text: text}}
		return canonMsg, nil
	}

	// Array content → parse as content blocks.
	blocks, err := parseContentBlocks(msg.Content)
	if err != nil {
		return canonMsg, err
	}

	for _, b := range blocks {
		switch b.Type {
		case "text":
			canonMsg.Blocks = append(canonMsg.Blocks, CanonicalBlock{Type: BlockText, Text: b.Text})
		case "image":
			cb := CanonicalBlock{Type: BlockImage}
			if b.Source != nil {
				if b.Source.Type == "base64" {
					cb.ImageMediaType = b.Source.MediaType
					cb.ImageData = b.Source.Data
				} else {
					cb.ImageURL = b.Source.URL
				}
			}
			canonMsg.Blocks = append(canonMsg.Blocks, cb)
		case "document":
			cb := CanonicalBlock{Type: BlockDocument}
			if b.Source != nil {
				if b.Source.Type == "base64" {
					cb.DocMediaType = b.Source.MediaType
					cb.DocData = b.Source.Data
				} else {
					cb.DocURL = b.Source.URL
				}
			}
			canonMsg.Blocks = append(canonMsg.Blocks, cb)
		case "tool_use":
			canonMsg.Blocks = append(canonMsg.Blocks, CanonicalBlock{
				Type:      BlockToolUse,
				ToolID:    b.ID,
				ToolName:  b.Name,
				ToolInput: b.Input,
			})
		case "tool_result":
			canonMsg.Blocks = append(canonMsg.Blocks, CanonicalBlock{
				Type:           BlockToolResult,
				ToolUseID:      b.ToolUseID,
				ToolResultText: extractToolResultText(b),
			})
		case "thinking":
			canonMsg.Blocks = append(canonMsg.Blocks, CanonicalBlock{
				Type:         BlockThinking,
				ThinkingText: b.Thinking,
			})
		}
	}

	return canonMsg, nil
}

// anthropicToolChoiceToCanonical converts the Anthropic tool_choice interface to canonical.
func anthropicToolChoiceToCanonical(tc any) (*CanonicalToolChoice, error) {
	switch v := tc.(type) {
	case map[string]string:
		return anthropicToolChoiceMapToCanonical(v["type"], v["name"]), nil
	case map[string]any:
		t, _ := v["type"].(string)
		name, _ := v["name"].(string)
		return anthropicToolChoiceMapToCanonical(t, name), nil
	default:
		return nil, fmt.Errorf("unsupported tool_choice type")
	}
}

func anthropicToolChoiceMapToCanonical(typ, name string) *CanonicalToolChoice {
	switch typ {
	case "any":
		return &CanonicalToolChoice{Mode: ToolChoiceRequired}
	case "tool":
		return &CanonicalToolChoice{Mode: ToolChoiceSpecific, FunctionName: name}
	default:
		return &CanonicalToolChoice{Mode: ToolChoiceAuto}
	}
}

// CanonicalToAnthropicRequest converts a canonical request to Anthropic format.
func CanonicalToAnthropicRequest(canon *CanonicalRequest) (*anthropic.MessagesRequest, error) {
	if canon == nil {
		return nil, fmt.Errorf("canonical request must not be nil")
	}

	result := &anthropic.MessagesRequest{
		Model:         canon.Model,
		Temperature:   canon.Temperature,
		TopP:          canon.TopP,
		TopK:          canon.TopK,
		StopSequences: canon.StopSequences,
		Stream:        canon.Stream,
	}

	if canon.MaxTokens != nil {
		result.MaxTokens = *canon.MaxTokens
	} else {
		result.MaxTokens = defaultMaxTokens
	}

	if canon.System != "" {
		result.System = canon.System
	}

	// Convert messages.
	for _, msg := range canon.Messages {
		// Validate: Anthropic does not support audio content.
		for _, b := range msg.Blocks {
			if b.Type == BlockAudio {
				return nil, fmt.Errorf("input_audio content type is not supported by Anthropic upstream")
			}
		}
		anthMsg := canonicalMessageToAnthropic(msg)
		result.Messages = append(result.Messages, anthMsg)
	}

	// Convert tools.
	for _, t := range canon.Tools {
		schema := t.Parameters
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		result.Tools = append(result.Tools, anthropic.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}

	// Convert tool_choice.
	if canon.ToolChoice != nil {
		result.ToolChoice = canonicalToolChoiceToAnthropic(canon.ToolChoice)
	}

	// Convert thinking.
	if canon.Thinking != nil && canon.Thinking.Enabled {
		budget := canon.Thinking.BudgetTokens
		if budget == 0 && canon.Thinking.Effort != "" {
			budget = effortToBudgetTokens(canon.Thinking.Effort, result.MaxTokens)
		}
		if budget > 0 {
			result.Thinking = &anthropic.ThinkingConfig{
				Type:         "enabled",
				BudgetTokens: budget,
			}
		}
	}

	return result, nil
}

// canonicalMessageToAnthropic converts a canonical message to an Anthropic message.
func canonicalMessageToAnthropic(msg CanonicalMessage) anthropic.Message {
	anthMsg := anthropic.Message{Role: msg.Role}

	// Optimization: single text block → string content.
	if len(msg.Blocks) == 1 && msg.Blocks[0].Type == BlockText {
		anthMsg.Content = msg.Blocks[0].Text
		return anthMsg
	}

	var blocks []anthropic.ContentBlock
	for _, b := range msg.Blocks {
		switch b.Type {
		case BlockText:
			blocks = append(blocks, anthropic.ContentBlock{Type: "text", Text: b.Text})
		case BlockImage:
			cb := anthropic.ContentBlock{Type: "image"}
			if b.ImageData != "" {
				cb.Source = &anthropic.ImageSource{
					Type:      "base64",
					MediaType: b.ImageMediaType,
					Data:      b.ImageData,
				}
			} else if b.ImageURL != "" {
				cb.Source = &anthropic.ImageSource{
					Type: "url",
					URL:  b.ImageURL,
				}
			}
			blocks = append(blocks, cb)
		case BlockDocument:
			cb := anthropic.ContentBlock{Type: "document"}
			if b.DocData != "" {
				cb.Source = &anthropic.ImageSource{
					Type:      "base64",
					MediaType: b.DocMediaType,
					Data:      b.DocData,
				}
			} else if b.DocURL != "" {
				cb.Source = &anthropic.ImageSource{
					Type: "url",
					URL:  b.DocURL,
				}
			}
			blocks = append(blocks, cb)
		case BlockToolUse:
			blocks = append(blocks, anthropic.ContentBlock{
				Type:  "tool_use",
				ID:    sanitizeAnthropicToolID(b.ToolID),
				Name:  b.ToolName,
				Input: b.ToolInput,
			})
		case BlockToolResult:
			blocks = append(blocks, anthropic.ContentBlock{
				Type:      "tool_result",
				ToolUseID: sanitizeAnthropicToolID(b.ToolUseID),
				Content:   b.ToolResultText,
			})
		case BlockThinking:
			blocks = append(blocks, anthropic.ContentBlock{
				Type:     "thinking",
				Thinking: b.ThinkingText,
			})
		}
	}

	anthMsg.Content = blocks
	return anthMsg
}

// canonicalToolChoiceToAnthropic converts canonical tool choice to Anthropic format.
func canonicalToolChoiceToAnthropic(tc *CanonicalToolChoice) any {
	switch tc.Mode {
	case ToolChoiceRequired:
		return map[string]string{"type": "any"}
	case ToolChoiceSpecific:
		return map[string]string{"type": "tool", "name": tc.FunctionName}
	case ToolChoiceNone:
		return nil
	default:
		return map[string]string{"type": "auto"}
	}
}

// AnthropicResponseToCanonical converts an Anthropic response to canonical form.
func AnthropicResponseToCanonical(resp *anthropic.MessagesResponse) *CanonicalResponse {
	canon := &CanonicalResponse{
		ID:    resp.ID,
		Model: resp.Model,
		Usage: CanonicalUsage{
			InputTokens:              resp.Usage.InputTokens,
			OutputTokens:             resp.Usage.OutputTokens,
			CacheCreationInputTokens: resp.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     resp.Usage.CacheReadInputTokens,
		},
	}

	// Map stop reason.
	canon.StopReason, canon.RawStopReason = anthropicStopReasonToCanonical(resp.StopReason)

	// Convert content blocks.
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			canon.Content = append(canon.Content, CanonicalBlock{Type: BlockText, Text: b.Text})
		case "thinking":
			canon.Content = append(canon.Content, CanonicalBlock{Type: BlockThinking, ThinkingText: b.Thinking})
		case "tool_use":
			canon.Content = append(canon.Content, CanonicalBlock{
				Type:      BlockToolUse,
				ToolID:    b.ID,
				ToolName:  b.Name,
				ToolInput: b.Input,
			})
		}
	}

	return canon
}

// CanonicalToAnthropicResponse converts a canonical response to Anthropic format.
func CanonicalToAnthropicResponse(canon *CanonicalResponse, model string) *anthropic.MessagesResponse {
	resp := &anthropic.MessagesResponse{
		ID:    canon.ID,
		Type:  "message",
		Role:  "assistant",
		Model: model,
		Usage: anthropic.Usage{
			InputTokens:              canon.Usage.InputTokens,
			OutputTokens:             canon.Usage.OutputTokens,
			CacheCreationInputTokens: canon.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     canon.Usage.CacheReadInputTokens,
		},
	}

	// Convert blocks.
	for _, b := range canon.Content {
		switch b.Type {
		case BlockText:
			resp.Content = append(resp.Content, anthropic.ContentBlock{Type: "text", Text: b.Text})
		case BlockThinking:
			resp.Content = append(resp.Content, anthropic.ContentBlock{Type: "thinking", Thinking: b.ThinkingText})
		case BlockToolUse:
			resp.Content = append(resp.Content, anthropic.ContentBlock{
				Type:  "tool_use",
				ID:    sanitizeAnthropicToolID(b.ToolID),
				Name:  b.ToolName,
				Input: b.ToolInput,
			})
		case BlockRefusal:
			// Anthropic has no refusal block; emit as text.
			resp.Content = append(resp.Content, anthropic.ContentBlock{Type: "text", Text: b.RefusalText})
		}
	}

	// Map stop reason.
	if canon.StopReason == StopReasonUnset {
		resp.StopReason = nil
	} else if canon.StopReason == StopReasonUnknown && canon.RawStopReason != nil {
		resp.StopReason = canon.RawStopReason
	} else {
		reason := canonicalStopReasonToAnthropic(canon.StopReason)
		resp.StopReason = &reason
	}

	return resp
}
