// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"strings"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
)

// ResponsesToAnthropicResponse translates a Responses API response body
// to an Anthropic MessagesResponse.
func ResponsesToAnthropicResponse(body []byte, model string) *anthropic.MessagesResponse {
	var resp ResponsesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return &anthropic.MessagesResponse{
			Type: "error",
		}
	}

	anthResp := &anthropic.MessagesResponse{
		ID:    strings.TrimPrefix(resp.ID, "resp_"),
		Type:  "message",
		Role:  "assistant",
		Model: model,
	}

	// Convert output items to content blocks.
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, content := range item.Content {
				switch content.Type {
				case "output_text":
					anthResp.Content = append(anthResp.Content, anthropic.ContentBlock{
						Type: "text",
						Text: content.Text,
					})
				case "refusal":
					anthResp.Content = append(anthResp.Content, anthropic.ContentBlock{
						Type: "text",
						Text: content.Refusal,
					})
				}
			}
		case "reasoning":
			// Convert reasoning output items to thinking content blocks.
			for _, s := range item.Summary {
				anthResp.Content = append(anthResp.Content, anthropic.ContentBlock{
					Type:     "thinking",
					Thinking: s.Text,
				})
			}
		case "function_call":
			var input interface{}
			if item.Arguments != "" {
				if err := json.Unmarshal([]byte(item.Arguments), &input); err != nil {
					input = map[string]interface{}{}
				}
			}
			anthResp.Content = append(anthResp.Content, anthropic.ContentBlock{
				Type:  "tool_use",
				ID:    item.CallID,
				Name:  item.Name,
				Input: input,
			})
		}
	}

	// Map status to stop_reason.
	anthResp.StopReason = responsesStatusToAnthropicStopReason(resp.Status, resp.Output)

	// Map usage.
	if resp.Usage != nil {
		anthResp.Usage = anthropic.Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
		}
		if resp.Usage.InputTokensDetails != nil {
			anthResp.Usage.CacheReadInputTokens = resp.Usage.InputTokensDetails.CachedTokens
		}
	}

	return anthResp
}

// responsesStatusToAnthropicStopReason maps Responses API status to Anthropic stop_reason.
func responsesStatusToAnthropicStopReason(status string, output []OutputItem) *string {
	// Check for tool use.
	for _, item := range output {
		if item.Type == "function_call" {
			reason := "tool_use"
			return &reason
		}
	}

	var reason string
	switch status {
	case "completed":
		reason = "end_turn"
	case "incomplete":
		reason = "max_tokens"
	default:
		reason = "end_turn"
	}
	return &reason
}
