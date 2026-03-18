// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"testing"

	"github.com/seckatie/glitchgate/internal/provider/openai"
	"github.com/stretchr/testify/require"
)

func TestResponsesToAnthropic_NilRequest(t *testing.T) {
	t.Parallel()
	_, err := ResponsesToAnthropic(nil, "claude-sonnet-4-20250514")
	require.Error(t, err)
	require.Contains(t, err.Error(), "must not be nil")
}

func TestResponsesToAnthropic_StringInput(t *testing.T) {
	t.Parallel()
	input, _ := json.Marshal("Hello, Claude!")
	req := &openai.ResponsesRequest{
		Model: "test-model",
		Input: input,
	}

	result, err := ResponsesToAnthropic(req, "claude-sonnet-4-20250514")
	require.NoError(t, err)
	require.Equal(t, "claude-sonnet-4-20250514", result.Model)
	require.Len(t, result.Messages, 1)
	require.Equal(t, "user", result.Messages[0].Role)
	require.Equal(t, "Hello, Claude!", result.Messages[0].Content)
}

func TestResponsesToAnthropic_Instructions(t *testing.T) {
	t.Parallel()
	input, _ := json.Marshal("Hi")
	instructions := "You are a helpful assistant."
	req := &openai.ResponsesRequest{
		Model:        "test-model",
		Input:        input,
		Instructions: &instructions,
	}

	result, err := ResponsesToAnthropic(req, "claude-sonnet-4-20250514")
	require.NoError(t, err)
	require.Equal(t, "You are a helpful assistant.", result.System)
}

func TestResponsesToAnthropic_MaxTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		maxOutputTokens   *int
		expectedMaxTokens int
	}{
		{
			name:              "explicit max tokens",
			maxOutputTokens:   intPtr(500),
			expectedMaxTokens: 500,
		},
		{
			name:              "default max tokens",
			maxOutputTokens:   nil,
			expectedMaxTokens: defaultMaxTokens,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input, _ := json.Marshal("Hi")
			req := &openai.ResponsesRequest{
				Model:           "test-model",
				Input:           input,
				MaxOutputTokens: tc.maxOutputTokens,
			}

			result, err := ResponsesToAnthropic(req, "claude-sonnet-4-20250514")
			require.NoError(t, err)
			require.Equal(t, tc.expectedMaxTokens, result.MaxTokens)
		})
	}
}

func TestResponsesToAnthropic_MessageInput(t *testing.T) {
	t.Parallel()
	items := []openai.InputItem{
		{
			Type:    "message",
			Role:    "user",
			Content: json.RawMessage(`"What is 2+2?"`),
		},
	}
	input, _ := json.Marshal(items)
	req := &openai.ResponsesRequest{
		Model: "test-model",
		Input: input,
	}

	result, err := ResponsesToAnthropic(req, "claude-sonnet-4-20250514")
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)
	require.Equal(t, "user", result.Messages[0].Role)
	require.Equal(t, "What is 2+2?", result.Messages[0].Content)
}

func TestResponsesToAnthropic_FunctionCallInput(t *testing.T) {
	t.Parallel()
	items := []openai.InputItem{
		{Type: "message", Role: "user", Content: json.RawMessage(`"What's the weather?"`)},
		{Type: "function_call", CallID: "call_123", Name: "get_weather", Arguments: `{"location":"SF"}`},
		{Type: "function_call_output", CallID: "call_123", Output: `{"temp":70}`},
	}
	input, _ := json.Marshal(items)
	req := &openai.ResponsesRequest{
		Model: "test-model",
		Input: input,
	}

	result, err := ResponsesToAnthropic(req, "claude-sonnet-4-20250514")
	require.NoError(t, err)
	require.Len(t, result.Messages, 3)

	// User message.
	require.Equal(t, "user", result.Messages[0].Role)

	// Function call → assistant with tool_use.
	require.Equal(t, "assistant", result.Messages[1].Role)

	// Function call output → user with tool_result.
	require.Equal(t, "user", result.Messages[2].Role)
}

func TestResponsesToAnthropic_Tools(t *testing.T) {
	t.Parallel()
	params := json.RawMessage(`{"type":"object","properties":{"location":{"type":"string"}}}`)
	input, _ := json.Marshal("Hi")
	req := &openai.ResponsesRequest{
		Model: "test-model",
		Input: input,
		Tools: []openai.ResponsesTool{
			{Type: "function", Name: "get_weather", Description: "Get weather", Parameters: params},
		},
	}

	result, err := ResponsesToAnthropic(req, "claude-sonnet-4-20250514")
	require.NoError(t, err)
	require.Len(t, result.Tools, 1)
	require.Equal(t, "get_weather", result.Tools[0].Name)
	require.Equal(t, "Get weather", result.Tools[0].Description)
}

func TestResponsesToAnthropic_UnsupportedToolTypeIsSkipped(t *testing.T) {
	t.Parallel()
	input, _ := json.Marshal("Hi")
	req := &openai.ResponsesRequest{
		Model: "test-model",
		Input: input,
		Tools: []openai.ResponsesTool{
			{Type: "web_search", Name: "web_search"},
		},
	}

	result, err := ResponsesToAnthropic(req, "claude-sonnet-4-20250514")
	require.NoError(t, err)
	require.Empty(t, result.Tools)
}

func TestResponsesToAnthropic_ToolChoice(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		toolChoice interface{}
		expected   interface{}
	}{
		{
			name:       "auto string",
			toolChoice: "auto",
			expected:   map[string]string{"type": "auto"},
		},
		{
			name:       "required string",
			toolChoice: "required",
			expected:   map[string]string{"type": "any"},
		},
		{
			name:       "none string",
			toolChoice: "none",
			expected:   nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input, _ := json.Marshal("Hi")
			req := &openai.ResponsesRequest{
				Model:      "test-model",
				Input:      input,
				ToolChoice: tc.toolChoice,
				Tools:      []openai.ResponsesTool{{Type: "function", Name: "f"}},
			}

			result, err := ResponsesToAnthropic(req, "claude-sonnet-4-20250514")
			require.NoError(t, err)
			require.Equal(t, tc.expected, result.ToolChoice)
		})
	}
}

func TestResponsesToAnthropic_InputFileError(t *testing.T) {
	t.Parallel()
	items := []openai.InputItem{
		{Type: "input_file", FileData: "base64data"},
	}
	input, _ := json.Marshal(items)
	req := &openai.ResponsesRequest{
		Model: "test-model",
		Input: input,
	}

	_, err := ResponsesToAnthropic(req, "claude-sonnet-4-20250514")
	require.Error(t, err)
	require.Contains(t, err.Error(), "input_file")
}

func TestResponsesToAnthropic_InputAudioError(t *testing.T) {
	t.Parallel()
	items := []openai.InputItem{
		{Type: "input_audio", Data: "base64audio", Format: "wav"},
	}
	input, _ := json.Marshal(items)
	req := &openai.ResponsesRequest{
		Model: "test-model",
		Input: input,
	}

	_, err := ResponsesToAnthropic(req, "claude-sonnet-4-20250514")
	require.Error(t, err)
	require.Contains(t, err.Error(), "input_audio")
}

func TestResponsesToAnthropic_ReasoningEffort(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		effort       *string
		maxTokens    *int
		wantThinking bool
		wantBudget   int
	}{
		{
			name:         "high effort",
			effort:       strPtr("high"),
			maxTokens:    intPtr(16384),
			wantThinking: true,
			wantBudget:   10000,
		},
		{
			name:         "medium effort",
			effort:       strPtr("medium"),
			maxTokens:    intPtr(16384),
			wantThinking: true,
			wantBudget:   5000,
		},
		{
			name:         "low effort",
			effort:       strPtr("low"),
			maxTokens:    intPtr(16384),
			wantThinking: true,
			wantBudget:   1024,
		},
		{
			name:         "high capped by maxTokens",
			effort:       strPtr("high"),
			maxTokens:    intPtr(3000),
			wantThinking: true,
			wantBudget:   2999,
		},
		{
			name:         "nil effort, no thinking",
			effort:       nil,
			maxTokens:    intPtr(16384),
			wantThinking: false,
		},
		{
			name:         "empty effort, no thinking",
			effort:       strPtr(""),
			maxTokens:    intPtr(16384),
			wantThinking: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input, _ := json.Marshal("Think about this.")
			var reasoning *openai.Reasoning
			if tc.effort != nil {
				reasoning = &openai.Reasoning{Effort: tc.effort}
			}
			req := &openai.ResponsesRequest{
				Model:           "test-model",
				Input:           input,
				MaxOutputTokens: tc.maxTokens,
				Reasoning:       reasoning,
			}

			result, err := ResponsesToAnthropic(req, "claude-sonnet-4-20250514")
			require.NoError(t, err)

			if tc.wantThinking {
				require.NotNil(t, result.Thinking)
				require.Equal(t, "enabled", result.Thinking.Type)
				require.Equal(t, tc.wantBudget, result.Thinking.BudgetTokens)
			} else {
				require.Nil(t, result.Thinking)
			}
		})
	}
}

func TestResponsesToAnthropic_Streaming(t *testing.T) {
	t.Parallel()
	streaming := true
	input, _ := json.Marshal("Hi")
	req := &openai.ResponsesRequest{
		Model:  "test-model",
		Input:  input,
		Stream: &streaming,
	}

	result, err := ResponsesToAnthropic(req, "claude-sonnet-4-20250514")
	require.NoError(t, err)
	require.True(t, result.Stream)
}

func TestResponsesToAnthropic_Temperature(t *testing.T) {
	t.Parallel()
	temp := 0.7
	topP := 0.9
	input, _ := json.Marshal("Hi")
	req := &openai.ResponsesRequest{
		Model:       "test-model",
		Input:       input,
		Temperature: &temp,
		TopP:        &topP,
	}

	result, err := ResponsesToAnthropic(req, "claude-sonnet-4-20250514")
	require.NoError(t, err)
	require.NotNil(t, result.Temperature)
	require.InDelta(t, 0.7, *result.Temperature, 0.001)
	require.NotNil(t, result.TopP)
	require.InDelta(t, 0.9, *result.TopP, 0.001)
}

func TestAnthropicToResponsesResponse_TextOnly(t *testing.T) {
	t.Parallel()
	body := `{
		"id": "msg_123",
		"content": [{"type": "text", "text": "Hello!"}],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 10, "output_tokens": 5}
	}`

	resp := AnthropicToResponsesResponse([]byte(body), "test-model")
	require.Equal(t, "response", resp.Object)
	require.Equal(t, "completed", resp.Status)
	require.Equal(t, "test-model", resp.Model)
	require.Len(t, resp.Output, 1)
	require.Equal(t, "message", resp.Output[0].Type)
	require.Equal(t, "assistant", resp.Output[0].Role)
	require.Len(t, resp.Output[0].Content, 1)
	require.Equal(t, "output_text", resp.Output[0].Content[0].Type)
	require.Equal(t, "Hello!", resp.Output[0].Content[0].Text)
	require.NotNil(t, resp.Usage)
	require.Equal(t, int64(10), resp.Usage.InputTokens)
	require.Equal(t, int64(5), resp.Usage.OutputTokens)
	require.Equal(t, int64(15), resp.Usage.TotalTokens)
}

func TestAnthropicToResponsesResponse_ToolUse(t *testing.T) {
	t.Parallel()
	body := `{
		"id": "msg_456",
		"content": [
			{"type": "text", "text": "Let me check."},
			{"type": "tool_use", "id": "tu_1", "name": "get_weather", "input": {"location": "SF"}}
		],
		"stop_reason": "tool_use",
		"usage": {"input_tokens": 20, "output_tokens": 15}
	}`

	resp := AnthropicToResponsesResponse([]byte(body), "test-model")
	require.Equal(t, "completed", resp.Status)
	require.Len(t, resp.Output, 2)

	// First item: message with text.
	require.Equal(t, "message", resp.Output[0].Type)
	require.Len(t, resp.Output[0].Content, 1)
	require.Equal(t, "Let me check.", resp.Output[0].Content[0].Text)

	// Second item: function_call.
	require.Equal(t, "function_call", resp.Output[1].Type)
	require.Equal(t, "get_weather", resp.Output[1].Name)
	require.Equal(t, "tu_1", resp.Output[1].CallID)
}

func TestAnthropicToResponsesResponse_CacheTokens(t *testing.T) {
	t.Parallel()
	body := `{
		"id": "msg_789",
		"content": [{"type": "text", "text": "Hi"}],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 100, "output_tokens": 10, "cache_read_input_tokens": 50}
	}`

	resp := AnthropicToResponsesResponse([]byte(body), "test-model")
	require.NotNil(t, resp.Usage)
	require.NotNil(t, resp.Usage.InputTokensDetails)
	require.Equal(t, int64(50), resp.Usage.InputTokensDetails.CachedTokens)
}

func TestAnthropicToResponsesResponse_MaxTokens(t *testing.T) {
	t.Parallel()
	body := `{
		"id": "msg_001",
		"content": [{"type": "text", "text": "partial..."}],
		"stop_reason": "max_tokens",
		"usage": {"input_tokens": 10, "output_tokens": 100}
	}`

	resp := AnthropicToResponsesResponse([]byte(body), "test-model")
	require.Equal(t, "incomplete", resp.Status)
}

func TestAnthropicToResponsesResponse_InvalidJSON(t *testing.T) {
	t.Parallel()
	resp := AnthropicToResponsesResponse([]byte("not json"), "test-model")
	require.Equal(t, "failed", resp.Status)
	require.NotNil(t, resp.Error)
	require.Equal(t, "server_error", resp.Error.Code)
}

func TestAnthropicToResponsesResponse_ThinkingBlocks(t *testing.T) {
	body := `{
		"id": "msg_think",
		"content": [
			{"type": "thinking", "thinking": "Let me reason about this carefully..."},
			{"type": "text", "text": "The answer is 42."}
		],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 100, "output_tokens": 50}
	}`

	resp := AnthropicToResponsesResponse([]byte(body), "test-model")
	require.Equal(t, "completed", resp.Status)

	// Should have 2 output items: reasoning + message.
	require.Len(t, resp.Output, 2)

	// First item: reasoning.
	require.Equal(t, "reasoning", resp.Output[0].Type)
	require.Len(t, resp.Output[0].Summary, 1)
	require.Equal(t, "summary_text", resp.Output[0].Summary[0].Type)
	require.Equal(t, "Let me reason about this carefully...", resp.Output[0].Summary[0].Text)

	// Second item: message.
	require.Equal(t, "message", resp.Output[1].Type)
	require.Len(t, resp.Output[1].Content, 1)
	require.Equal(t, "The answer is 42.", resp.Output[1].Content[0].Text)

	// Usage should not synthesize openai.OutputTokensDetails from Anthropic thinking blocks.
	require.NotNil(t, resp.Usage)
	require.Nil(t, resp.Usage.OutputTokensDetails)
}

func TestAnthropicToResponsesResponse_NoThinking(t *testing.T) {
	body := `{
		"id": "msg_nothink",
		"content": [{"type": "text", "text": "Simple response."}],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 10, "output_tokens": 5}
	}`

	resp := AnthropicToResponsesResponse([]byte(body), "test-model")
	require.Len(t, resp.Output, 1)
	require.Equal(t, "message", resp.Output[0].Type)
	require.Nil(t, resp.Usage.OutputTokensDetails)
}

func TestResponsesToAnthropicResponse_ReasoningItems(t *testing.T) {
	body := `{
		"id": "resp_123",
		"object": "response",
		"status": "completed",
		"output": [
			{
				"type": "reasoning",
				"id": "rs_123",
				"summary": [{"type": "summary_text", "text": "I thought about it..."}]
			},
			{
				"type": "message",
				"id": "msg_123",
				"role": "assistant",
				"content": [{"type": "output_text", "text": "Here is my answer."}]
			}
		],
		"usage": {"input_tokens": 100, "output_tokens": 50, "total_tokens": 150}
	}`

	anthResp := ResponsesToAnthropicResponse([]byte(body), "claude-sonnet-4-20250514")
	require.Equal(t, "message", anthResp.Type)
	require.Equal(t, "assistant", anthResp.Role)

	// Should have 2 content blocks: thinking + text.
	require.Len(t, anthResp.Content, 2)
	require.Equal(t, "thinking", anthResp.Content[0].Type)
	require.Equal(t, "I thought about it...", anthResp.Content[0].Thinking)
	require.Equal(t, "text", anthResp.Content[1].Type)
	require.Equal(t, "Here is my answer.", anthResp.Content[1].Text)
}

func TestResponsesToAnthropicResponse_NoReasoning(t *testing.T) {
	body := `{
		"id": "resp_456",
		"object": "response",
		"status": "completed",
		"output": [
			{
				"type": "message",
				"id": "msg_456",
				"role": "assistant",
				"content": [{"type": "output_text", "text": "Hello!"}]
			}
		],
		"usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}
	}`

	anthResp := ResponsesToAnthropicResponse([]byte(body), "claude-sonnet-4-20250514")
	require.Len(t, anthResp.Content, 1)
	require.Equal(t, "text", anthResp.Content[0].Type)
	require.Equal(t, "Hello!", anthResp.Content[0].Text)
}
