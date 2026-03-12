// Package translate converts between Anthropic and OpenAI API formats.
package translate

import (
	"encoding/json"
	"strings"
	"time"

	"codeberg.org/kglitchy/glitchgate/internal/provider/anthropic"
)

// AnthropicToOpenAI translates an Anthropic MessagesResponse into an
// OpenAI ChatCompletionResponse. The model parameter is the client-facing
// model name to include in the response.
func AnthropicToOpenAI(resp *anthropic.MessagesResponse, model string) *ChatCompletionResponse {
	result := &ChatCompletionResponse{
		ID:      "chatcmpl-" + resp.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
	}

	// Build the message from content blocks.
	msg := &ChatMessage{
		Role: "assistant",
	}

	var textParts []string
	var toolCalls []ToolCall

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "tool_use":
			args, err := json.Marshal(block.Input)
			if err != nil {
				args = []byte("{}")
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      block.Name,
					Arguments: string(args),
				},
			})
		}
	}

	if len(textParts) > 0 {
		combined := strings.Join(textParts, "")
		msg.Content = combined
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}

	// Map stop reason.
	finishReason := mapStopReason(resp.StopReason)

	result.Choices = []Choice{
		{
			Index:        0,
			Message:      msg,
			FinishReason: finishReason,
		},
	}

	// Map usage.
	result.Usage = &OpenAIUsage{
		PromptTokens:     resp.Usage.InputTokens,
		CompletionTokens: resp.Usage.OutputTokens,
		TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
	}

	return result
}

// mapStopReason translates an Anthropic stop_reason to an OpenAI finish_reason.
func mapStopReason(stopReason *string) *string {
	if stopReason == nil {
		return nil
	}
	var reason string
	switch *stopReason {
	case "end_turn":
		reason = "stop"
	case "max_tokens":
		reason = "length"
	case "stop_sequence":
		reason = "stop"
	case "tool_use":
		reason = "tool_calls"
	default:
		reason = *stopReason
	}
	return &reason
}

// AnthropicErrorToOpenAI translates an Anthropic error response body
// into an OpenAI-formatted error response.
func AnthropicErrorToOpenAI(body []byte) ([]byte, error) {
	var anthErr anthropic.ErrorResponse
	if err := json.Unmarshal(body, &anthErr); err != nil {
		// If we can't parse the Anthropic error, wrap the raw message.
		oaiErr := OpenAIErrorResponse{
			Error: OpenAIError{
				Message: string(body),
				Type:    "api_error",
			},
		}
		return json.Marshal(oaiErr)
	}

	oaiErr := OpenAIErrorResponse{
		Error: OpenAIError{
			Message: anthErr.Error.Message,
			Type:    mapAnthropicErrorType(anthErr.Error.Type),
			Code:    anthErr.Error.Type,
		},
	}
	return json.Marshal(oaiErr)
}

// mapAnthropicErrorType maps Anthropic error types to OpenAI error types.
func mapAnthropicErrorType(errType string) string {
	switch errType {
	case "authentication_error":
		return "authentication_error"
	case "invalid_request_error":
		return "invalid_request_error"
	case "not_found_error":
		return "not_found_error"
	case "rate_limit_error":
		return "rate_limit_error"
	case "overloaded_error":
		return "server_error"
	default:
		return "api_error"
	}
}
