package proxy

import (
	"encoding/json"
	"strings"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
)

// extractResponsesTokens parses token usage from the response.completed event's usage field.
func extractResponsesTokens(data string, result *StreamResult) {
	if !strings.Contains(data, "response.completed") {
		return
	}

	var event struct {
		Type     string `json:"type"`
		Response *struct {
			Usage *struct {
				InputTokens        int64 `json:"input_tokens"`
				OutputTokens       int64 `json:"output_tokens"`
				InputTokensDetails *struct {
					CachedTokens int64 `json:"cached_tokens"`
				} `json:"input_tokens_details"`
				OutputTokensDetails *struct {
					ReasoningTokens int64 `json:"reasoning_tokens"`
				} `json:"output_tokens_details"`
			} `json:"usage"`
		} `json:"response"`
	}

	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return
	}
	if event.Type == "response.completed" && event.Response != nil && event.Response.Usage != nil {
		result.InputTokens = event.Response.Usage.InputTokens
		result.OutputTokens = event.Response.Usage.OutputTokens
		if event.Response.Usage.InputTokensDetails != nil {
			result.CacheReadInputTokens = event.Response.Usage.InputTokensDetails.CachedTokens
			result.InputTokens -= result.CacheReadInputTokens
			if result.InputTokens < 0 {
				result.InputTokens = 0
			}
		}
		if event.Response.Usage.OutputTokensDetails != nil {
			result.ReasoningTokens = event.Response.Usage.OutputTokensDetails.ReasoningTokens
		}
	}
}

// extractOpenAITokens parses an OpenAI SSE data payload for token usage.
// OpenAI includes usage in a final chunk with a "usage" field.
// completion_tokens already includes reasoning_tokens (sub-breakdown, not additive).
// cached_tokens is a sub-breakdown of prompt_tokens and is subtracted to get net input.
func extractOpenAITokens(data string, result *StreamResult) {
	if !strings.Contains(data, "\"usage\"") {
		return
	}

	var chunk struct {
		Usage *struct {
			PromptTokens        int64 `json:"prompt_tokens"`
			CompletionTokens    int64 `json:"completion_tokens"`
			ReasoningTokens     int64 `json:"reasoning_tokens"`
			PromptTokensDetails *struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionTokensDetails *struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}

	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return
	}
	if chunk.Usage != nil {
		result.InputTokens = chunk.Usage.PromptTokens
		result.OutputTokens = chunk.Usage.CompletionTokens
		if chunk.Usage.PromptTokensDetails != nil {
			result.CacheReadInputTokens = chunk.Usage.PromptTokensDetails.CachedTokens
			result.InputTokens -= result.CacheReadInputTokens
			if result.InputTokens < 0 {
				result.InputTokens = 0
			}
		}
		if chunk.Usage.CompletionTokensDetails != nil {
			result.ReasoningTokens = chunk.Usage.CompletionTokensDetails.ReasoningTokens
		}
		if result.ReasoningTokens == 0 && chunk.Usage.ReasoningTokens > 0 {
			result.ReasoningTokens = chunk.Usage.ReasoningTokens
		}
	}
}

// extractAnthropicTokens parses SSE data payloads for token usage information.
// message_start contains input_tokens and cache token counts; message_delta contains output_tokens.
func extractAnthropicTokens(data string, result *StreamResult) {
	if !strings.Contains(data, "message_start") && !strings.Contains(data, "message_delta") {
		return
	}

	var envelope struct {
		Type    string `json:"type"`
		Message *struct {
			Usage *anthropic.Usage `json:"usage"`
		} `json:"message"`
		Usage *anthropic.DeltaUsage `json:"usage"`
	}

	if err := json.Unmarshal([]byte(data), &envelope); err != nil {
		return
	}

	switch envelope.Type {
	case "message_start":
		if envelope.Message != nil && envelope.Message.Usage != nil {
			result.InputTokens = envelope.Message.Usage.InputTokens
			result.CacheCreationInputTokens = envelope.Message.Usage.CacheCreationInputTokens
			result.CacheReadInputTokens = envelope.Message.Usage.CacheReadInputTokens
		}
	case "message_delta":
		if envelope.Usage != nil {
			result.OutputTokens = envelope.Usage.OutputTokens
			if envelope.Usage.InputTokens > 0 {
				result.InputTokens = envelope.Usage.InputTokens
			}
			if envelope.Usage.CacheCreationInputTokens > 0 {
				result.CacheCreationInputTokens = envelope.Usage.CacheCreationInputTokens
			}
			if envelope.Usage.CacheReadInputTokens > 0 {
				result.CacheReadInputTokens = envelope.Usage.CacheReadInputTokens
			}
		}
	}
}
