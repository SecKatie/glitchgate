// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"strings"

	"codeberg.org/kglitchy/glitchgate/internal/provider/anthropic"
)

// OpenAIToAnthropicResponse translates an OpenAI ChatCompletionResponse into
// an Anthropic MessagesResponse. This is the reverse of AnthropicToOpenAI and
// is used when returning responses from OpenAI-native providers to
// Anthropic-format clients.
func OpenAIToAnthropicResponse(resp *ChatCompletionResponse, model string) *anthropic.MessagesResponse {
	result := &anthropic.MessagesResponse{
		ID:    strings.TrimPrefix(resp.ID, "chatcmpl-"),
		Type:  "message",
		Role:  "assistant",
		Model: model,
	}

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		msg := choice.Message

		if msg != nil {
			// Convert text content to Anthropic text block.
			if text, ok := msg.Content.(string); ok && text != "" {
				result.Content = append(result.Content, anthropic.ContentBlock{
					Type: "text",
					Text: text,
				})
			}

			// Convert tool calls to Anthropic tool_use blocks.
			for _, tc := range msg.ToolCalls {
				var input interface{}
				if tc.Function.Arguments != "" {
					if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
						input = map[string]interface{}{}
					}
				}
				result.Content = append(result.Content, anthropic.ContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: input,
				})
			}
		}

		// Map OpenAI finish_reason to Anthropic stop_reason.
		result.StopReason = reverseMapStopReason(choice.FinishReason)
	}

	// Map usage.
	if resp.Usage != nil {
		result.Usage = anthropic.Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		}
	}

	return result
}

// reverseMapStopReason translates an OpenAI finish_reason to an Anthropic stop_reason.
func reverseMapStopReason(finishReason *string) *string {
	if finishReason == nil {
		return nil
	}
	var reason string
	switch *finishReason {
	case "stop":
		reason = "end_turn"
	case "length":
		reason = "max_tokens"
	case "tool_calls":
		reason = "tool_use"
	default:
		reason = *finishReason
	}
	return &reason
}
