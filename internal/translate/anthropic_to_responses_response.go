// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"codeberg.org/kglitchy/glitchgate/internal/provider/anthropic"
)

// AnthropicToResponsesResponse translates an Anthropic Messages response body
// to a Responses API response.
func AnthropicToResponsesResponse(body []byte, model string) *ResponsesResponse {
	var anthResp anthropic.MessagesResponse
	if err := json.Unmarshal(body, &anthResp); err != nil {
		return &ResponsesResponse{
			Object: "response",
			Status: "failed",
			Error: &ResponsesError{
				Code:    "server_error",
				Message: "Failed to parse upstream response",
			},
		}
	}

	resp := &ResponsesResponse{
		ID:        "resp_" + anthResp.ID,
		Object:    "response",
		CreatedAt: float64(time.Now().Unix()),
		Model:     model,
		Status:    anthropicStatusToResponses(anthResp.StopReason, anthResp.Content),
	}

	// Convert content blocks to output items.
	var output []OutputItem
	var textContents []OutputContent
	var reasoningItems []OutputItem

	for _, block := range anthResp.Content {
		switch block.Type {
		case "text":
			textContents = append(textContents, OutputContent{
				Type:        "output_text",
				Text:        block.Text,
				Annotations: []any{},
			})
		case "thinking":
			// Convert thinking blocks to reasoning output items.
			reasoningItems = append(reasoningItems, OutputItem{
				Type: "reasoning",
				ID:   "rs_" + anthResp.ID,
				Summary: []ReasoningSummary{{
					Type: "summary_text",
					Text: block.Thinking,
				}},
			})
		case "tool_use":
			args, err := json.Marshal(block.Input)
			if err != nil {
				args = []byte("{}")
			}
			output = append(output, OutputItem{
				Type:      "function_call",
				ID:        "fc_" + block.ID,
				CallID:    block.ID,
				Name:      block.Name,
				Arguments: string(args),
				Status:    "completed",
			})
		}
	}

	// Prepend reasoning items before message and tool calls.
	if len(reasoningItems) > 0 {
		output = append(reasoningItems, output...)
	}

	// Add message output item if there's text content.
	if len(textContents) > 0 {
		msgItem := OutputItem{
			Type:    "message",
			ID:      "msg_" + anthResp.ID,
			Role:    "assistant",
			Content: textContents,
			Status:  "completed",
		}
		// Insert message after reasoning items but before function calls.
		insertIdx := len(reasoningItems)
		output = slices.Insert(output, insertIdx, msgItem)
	}

	resp.Output = output

	// Map usage.
	resp.Usage = &ResponsesUsage{
		InputTokens:  anthResp.Usage.InputTokens,
		OutputTokens: anthResp.Usage.OutputTokens,
		TotalTokens:  anthResp.Usage.InputTokens + anthResp.Usage.OutputTokens,
	}
	if anthResp.Usage.CacheReadInputTokens > 0 {
		resp.Usage.InputTokensDetails = &InputTokensDetails{
			CachedTokens: anthResp.Usage.CacheReadInputTokens,
		}
	}

	return resp
}

// anthropicStatusToResponses maps an Anthropic stop_reason to a Responses API status.
func anthropicStatusToResponses(stopReason *string, content []anthropic.ContentBlock) string {
	if stopReason == nil {
		return "completed"
	}

	// Check if there are tool_use blocks.
	for _, block := range content {
		if block.Type == "tool_use" {
			return "completed" // tool use is a normal completion
		}
	}

	switch *stopReason {
	case "end_turn":
		return "completed"
	case "max_tokens":
		return "incomplete"
	case "tool_use":
		return "completed"
	default:
		return "completed"
	}
}

// OpenAIToResponsesResponse translates an OpenAI Chat Completions response body
// to a Responses API response.
func OpenAIToResponsesResponse(body []byte, model string) *ResponsesResponse {
	var ccResp ChatCompletionResponse
	if err := json.Unmarshal(body, &ccResp); err != nil {
		return &ResponsesResponse{
			Object: "response",
			Status: "failed",
			Error: &ResponsesError{
				Code:    "server_error",
				Message: "Failed to parse upstream response",
			},
		}
	}

	resp := &ResponsesResponse{
		ID:        "resp_" + strings.TrimPrefix(ccResp.ID, "chatcmpl-"),
		Object:    "response",
		CreatedAt: float64(ccResp.Created),
		Model:     model,
	}

	var output []OutputItem

	if len(ccResp.Choices) > 0 {
		choice := ccResp.Choices[0]

		// Map status from finish_reason.
		resp.Status = openAIFinishReasonToResponsesStatus(choice.FinishReason)

		msg := choice.Message
		if msg != nil {
			// Text content.
			var textContents []OutputContent
			if text, ok := msg.Content.(string); ok && text != "" {
				textContents = append(textContents, OutputContent{
					Type:        "output_text",
					Text:        text,
					Annotations: []any{},
				})
			}
			if msg.Refusal != "" {
				textContents = append(textContents, OutputContent{
					Type:    "refusal",
					Refusal: msg.Refusal,
				})
			}

			if len(textContents) > 0 {
				output = append(output, OutputItem{
					Type:    "message",
					ID:      "msg_" + strings.TrimPrefix(ccResp.ID, "chatcmpl-"),
					Role:    "assistant",
					Content: textContents,
					Status:  "completed",
				})
			}

			// Tool calls.
			for _, tc := range msg.ToolCalls {
				output = append(output, OutputItem{
					Type:      "function_call",
					ID:        "fc_" + tc.ID,
					CallID:    tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
					Status:    "completed",
				})
			}
		}
	} else {
		resp.Status = "completed"
	}

	resp.Output = output

	// Map usage.
	if ccResp.Usage != nil {
		resp.Usage = &ResponsesUsage{
			InputTokens:  ccResp.Usage.PromptTokens,
			OutputTokens: ccResp.Usage.CompletionTokens,
			TotalTokens:  ccResp.Usage.TotalTokens,
		}
		if ccResp.Usage.CompletionTokensDetails != nil && ccResp.Usage.CompletionTokensDetails.ReasoningTokens > 0 {
			resp.Usage.OutputTokensDetails = &OutputTokensDetails{
				ReasoningTokens: ccResp.Usage.CompletionTokensDetails.ReasoningTokens,
			}
		}
		if ccResp.Usage.PromptTokensDetails != nil && ccResp.Usage.PromptTokensDetails.CachedTokens > 0 {
			resp.Usage.InputTokensDetails = &InputTokensDetails{
				CachedTokens: ccResp.Usage.PromptTokensDetails.CachedTokens,
			}
		}
	}

	return resp
}

// openAIFinishReasonToResponsesStatus maps OpenAI finish_reason to Responses API status.
func openAIFinishReasonToResponsesStatus(finishReason *string) string {
	if finishReason == nil {
		return "in_progress"
	}
	switch *finishReason {
	case "stop":
		return "completed"
	case "length":
		return "incomplete"
	case "tool_calls":
		return "completed"
	case "content_filter":
		return "incomplete"
	default:
		return "completed"
	}
}
