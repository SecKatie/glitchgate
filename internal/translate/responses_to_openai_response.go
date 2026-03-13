// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"strings"
)

// ResponsesToOpenAIResponse translates a Responses API response body
// to a Chat Completions response.
func ResponsesToOpenAIResponse(body []byte, model string) *ChatCompletionResponse {
	var resp ResponsesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return &ChatCompletionResponse{
			Object: "chat.completion",
		}
	}

	ccResp := &ChatCompletionResponse{
		ID:      "chatcmpl-" + strings.TrimPrefix(resp.ID, "resp_"),
		Object:  "chat.completion",
		Created: int64(resp.CreatedAt),
		Model:   model,
	}

	msg := &ChatMessage{
		Role: "assistant",
	}

	var toolCalls []ToolCall

	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, content := range item.Content {
				switch content.Type {
				case "output_text":
					msg.Content = content.Text
				case "refusal":
					msg.Refusal = content.Refusal
				}
			}
		case "function_call":
			toolCalls = append(toolCalls, ToolCall{
				ID:   item.CallID,
				Type: "function",
				Function: FunctionCall{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})
		}
	}

	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}

	finishReason := responsesStatusToOpenAIFinishReason(resp.Status, resp.Output)
	ccResp.Choices = []Choice{{
		Index:        0,
		Message:      msg,
		FinishReason: finishReason,
	}}

	// Map usage.
	if resp.Usage != nil {
		ccResp.Usage = &OpenAIUsage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		}
		if resp.Usage.OutputTokensDetails != nil && resp.Usage.OutputTokensDetails.ReasoningTokens > 0 {
			ccResp.Usage.CompletionTokensDetails = &CompletionTokensDetails{
				ReasoningTokens: resp.Usage.OutputTokensDetails.ReasoningTokens,
			}
		}
		if resp.Usage.InputTokensDetails != nil && resp.Usage.InputTokensDetails.CachedTokens > 0 {
			ccResp.Usage.PromptTokensDetails = &PromptTokensDetails{
				CachedTokens: resp.Usage.InputTokensDetails.CachedTokens,
			}
		}
	}

	return ccResp
}

// responsesStatusToOpenAIFinishReason maps Responses API status to CC finish_reason.
func responsesStatusToOpenAIFinishReason(status string, output []OutputItem) *string {
	for _, item := range output {
		if item.Type == "function_call" {
			reason := "tool_calls"
			return &reason
		}
	}

	var reason string
	switch status {
	case "completed":
		reason = "stop"
	case "incomplete":
		reason = "length"
	default:
		reason = "stop"
	}
	return &reason
}
