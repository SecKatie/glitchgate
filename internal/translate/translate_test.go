package translate

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"codeberg.org/kglitchy/glitchgate/internal/provider/anthropic"
)

func TestOpenAIToAnthropic_BasicMessage(t *testing.T) {
	req := &ChatCompletionRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []ChatMessage{
			{Role: "user", Content: "Hello, Claude!"},
		},
		MaxTokens: intPtr(200),
	}

	result, err := OpenAIToAnthropic(req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "claude-sonnet-4-20250514", result.Model)
	require.Equal(t, 200, result.MaxTokens)
	require.Len(t, result.Messages, 1)
	require.Equal(t, "user", result.Messages[0].Role)
	require.Equal(t, "Hello, Claude!", result.Messages[0].Content)
	require.Nil(t, result.System)
}

func TestOpenAIToAnthropic_SystemExtraction(t *testing.T) {
	tests := []struct {
		name           string
		messages       []ChatMessage
		expectedSystem string
		expectedCount  int // number of non-system messages
	}{
		{
			name: "single system message",
			messages: []ChatMessage{
				{Role: "system", Content: "You are a helpful assistant."},
				{Role: "user", Content: "Hi"},
			},
			expectedSystem: "You are a helpful assistant.",
			expectedCount:  1,
		},
		{
			name: "multiple system messages concatenated",
			messages: []ChatMessage{
				{Role: "system", Content: "You are a helpful assistant."},
				{Role: "system", Content: "Be concise."},
				{Role: "user", Content: "Hi"},
			},
			expectedSystem: "You are a helpful assistant.\nBe concise.",
			expectedCount:  1,
		},
		{
			name: "no system message",
			messages: []ChatMessage{
				{Role: "user", Content: "Hi"},
			},
			expectedSystem: "",
			expectedCount:  1,
		},
		{
			name: "system between user messages",
			messages: []ChatMessage{
				{Role: "system", Content: "Be brief."},
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Hi"},
				{Role: "user", Content: "What?"},
			},
			expectedSystem: "Be brief.",
			expectedCount:  3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &ChatCompletionRequest{
				Model:     "claude-sonnet-4-20250514",
				Messages:  tc.messages,
				MaxTokens: intPtr(100),
			}

			result, err := OpenAIToAnthropic(req)
			require.NoError(t, err)
			require.NotNil(t, result)

			if tc.expectedSystem == "" {
				require.Nil(t, result.System)
			} else {
				require.Equal(t, tc.expectedSystem, result.System)
			}

			require.Len(t, result.Messages, tc.expectedCount)
		})
	}
}

func TestOpenAIToAnthropic_DefaultMaxTokens(t *testing.T) {
	tests := []struct {
		name              string
		maxTokens         *int
		expectedMaxTokens int
	}{
		{
			name:              "nil max_tokens defaults to 4096",
			maxTokens:         nil,
			expectedMaxTokens: 4096,
		},
		{
			name:              "explicit max_tokens respected",
			maxTokens:         intPtr(500),
			expectedMaxTokens: 500,
		},
		{
			name:              "zero max_tokens uses explicit zero",
			maxTokens:         intPtr(0),
			expectedMaxTokens: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &ChatCompletionRequest{
				Model: "claude-sonnet-4-20250514",
				Messages: []ChatMessage{
					{Role: "user", Content: "Hi"},
				},
				MaxTokens: tc.maxTokens,
			}

			result, err := OpenAIToAnthropic(req)
			require.NoError(t, err)
			require.Equal(t, tc.expectedMaxTokens, result.MaxTokens)
		})
	}
}

func TestOpenAIToAnthropic_NilRequest(t *testing.T) {
	result, err := OpenAIToAnthropic(nil)
	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "nil")
}

func TestOpenAIToAnthropic_OptionalParams(t *testing.T) {
	temp := 0.7
	topP := 0.9

	req := &ChatCompletionRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []ChatMessage{
			{Role: "user", Content: "Hi"},
		},
		MaxTokens:   intPtr(100),
		Temperature: &temp,
		TopP:        &topP,
		Stop:        "END",
		Stream:      true,
	}

	result, err := OpenAIToAnthropic(req)
	require.NoError(t, err)
	require.NotNil(t, result.Temperature)
	require.InDelta(t, 0.7, *result.Temperature, 0.001)
	require.NotNil(t, result.TopP)
	require.InDelta(t, 0.9, *result.TopP, 0.001)
	require.Equal(t, []string{"END"}, result.StopSequences)
	require.True(t, result.Stream)
}

func TestOpenAIToAnthropic_StopSequences(t *testing.T) {
	tests := []struct {
		name     string
		stop     interface{}
		expected []string
	}{
		{
			name:     "string stop",
			stop:     "STOP",
			expected: []string{"STOP"},
		},
		{
			name:     "array of strings stop",
			stop:     []string{"STOP", "END"},
			expected: []string{"STOP", "END"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &ChatCompletionRequest{
				Model: "claude-sonnet-4-20250514",
				Messages: []ChatMessage{
					{Role: "user", Content: "Hi"},
				},
				Stop: tc.stop,
			}

			result, err := OpenAIToAnthropic(req)
			require.NoError(t, err)
			require.Equal(t, tc.expected, result.StopSequences)
		})
	}
}

func TestOpenAIToAnthropic_ToolCallMessage(t *testing.T) {
	req := &ChatCompletionRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []ChatMessage{
			{Role: "user", Content: "What is the weather?"},
			{
				Role:    "assistant",
				Content: nil,
				ToolCalls: []ToolCall{
					{
						ID:   "call_123",
						Type: "function",
						Function: FunctionCall{
							Name:      "get_weather",
							Arguments: `{"location":"NYC"}`,
						},
					},
				},
			},
			{
				Role:       "tool",
				Content:    "72F and sunny",
				ToolCallID: "call_123",
			},
		},
	}

	result, err := OpenAIToAnthropic(req)
	require.NoError(t, err)
	require.Len(t, result.Messages, 3)
	// The tool result should become a user message with tool_result content block.
	require.Equal(t, "user", result.Messages[2].Role)
}

func TestAnthropicToOpenAI_StopReasons(t *testing.T) {
	tests := []struct {
		name                 string
		anthropicStopReason  *string
		expectedFinishReason *string
	}{
		{
			name:                 "end_turn maps to stop",
			anthropicStopReason:  strPtr("end_turn"),
			expectedFinishReason: strPtr("stop"),
		},
		{
			name:                 "max_tokens maps to length",
			anthropicStopReason:  strPtr("max_tokens"),
			expectedFinishReason: strPtr("length"),
		},
		{
			name:                 "stop_sequence maps to stop",
			anthropicStopReason:  strPtr("stop_sequence"),
			expectedFinishReason: strPtr("stop"),
		},
		{
			name:                 "tool_use maps to tool_calls",
			anthropicStopReason:  strPtr("tool_use"),
			expectedFinishReason: strPtr("tool_calls"),
		},
		{
			name:                 "nil maps to nil",
			anthropicStopReason:  nil,
			expectedFinishReason: nil,
		},
		{
			name:                 "unknown reason passed through",
			anthropicStopReason:  strPtr("custom_reason"),
			expectedFinishReason: strPtr("custom_reason"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			anthResp := &anthropic.MessagesResponse{
				ID:   "msg_stop_test",
				Type: "message",
				Role: "assistant",
				Content: []anthropic.ContentBlock{
					{Type: "text", Text: "Hello"},
				},
				Model:      "claude-sonnet-4-20250514",
				StopReason: tc.anthropicStopReason,
				Usage: anthropic.Usage{
					InputTokens:  10,
					OutputTokens: 5,
				},
			}

			result := AnthropicToOpenAI(anthResp, "claude-sonnet")
			require.NotNil(t, result)
			require.Len(t, result.Choices, 1)

			if tc.expectedFinishReason == nil {
				require.Nil(t, result.Choices[0].FinishReason)
			} else {
				require.NotNil(t, result.Choices[0].FinishReason)
				require.Equal(t, *tc.expectedFinishReason, *result.Choices[0].FinishReason)
			}
		})
	}
}

func TestAnthropicToOpenAI_Usage(t *testing.T) {
	tests := []struct {
		name                     string
		inputTokens              int64
		outputTokens             int64
		expectedPromptTokens     int64
		expectedCompletionTokens int64
		expectedTotalTokens      int64
	}{
		{
			name:                     "basic token mapping",
			inputTokens:              100,
			outputTokens:             50,
			expectedPromptTokens:     100,
			expectedCompletionTokens: 50,
			expectedTotalTokens:      150,
		},
		{
			name:                     "zero tokens",
			inputTokens:              0,
			outputTokens:             0,
			expectedPromptTokens:     0,
			expectedCompletionTokens: 0,
			expectedTotalTokens:      0,
		},
		{
			name:                     "large token counts",
			inputTokens:              100000,
			outputTokens:             50000,
			expectedPromptTokens:     100000,
			expectedCompletionTokens: 50000,
			expectedTotalTokens:      150000,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			anthResp := &anthropic.MessagesResponse{
				ID:         "msg_usage_test",
				Type:       "message",
				Role:       "assistant",
				Content:    []anthropic.ContentBlock{{Type: "text", Text: "Test"}},
				Model:      "claude-sonnet-4-20250514",
				StopReason: strPtr("end_turn"),
				Usage: anthropic.Usage{
					InputTokens:  tc.inputTokens,
					OutputTokens: tc.outputTokens,
				},
			}

			result := AnthropicToOpenAI(anthResp, "test-model")
			require.NotNil(t, result)
			require.NotNil(t, result.Usage)
			require.Equal(t, tc.expectedPromptTokens, result.Usage.PromptTokens)
			require.Equal(t, tc.expectedCompletionTokens, result.Usage.CompletionTokens)
			require.Equal(t, tc.expectedTotalTokens, result.Usage.TotalTokens)
		})
	}
}

func TestAnthropicToOpenAI_ResponseStructure(t *testing.T) {
	anthResp := &anthropic.MessagesResponse{
		ID:   "msg_struct_test",
		Type: "message",
		Role: "assistant",
		Content: []anthropic.ContentBlock{
			{Type: "text", Text: "Part 1"},
			{Type: "text", Text: "Part 2"},
		},
		Model:      "claude-sonnet-4-20250514",
		StopReason: strPtr("end_turn"),
		Usage: anthropic.Usage{
			InputTokens:  50,
			OutputTokens: 25,
		},
	}

	result := AnthropicToOpenAI(anthResp, "my-model")
	require.Equal(t, "chatcmpl-msg_struct_test", result.ID)
	require.Equal(t, "chat.completion", result.Object)
	require.Equal(t, "my-model", result.Model)
	require.Greater(t, result.Created, int64(0))
	require.Len(t, result.Choices, 1)
	require.Equal(t, 0, result.Choices[0].Index)
	require.NotNil(t, result.Choices[0].Message)
	require.Equal(t, "assistant", result.Choices[0].Message.Role)
	// Multiple text blocks should be concatenated.
	require.Equal(t, "Part 1Part 2", result.Choices[0].Message.Content)
}

func TestAnthropicToOpenAI_ToolUse(t *testing.T) {
	anthResp := &anthropic.MessagesResponse{
		ID:   "msg_tool_test",
		Type: "message",
		Role: "assistant",
		Content: []anthropic.ContentBlock{
			{Type: "text", Text: "Let me check."},
			{
				Type:  "tool_use",
				ID:    "toolu_123",
				Name:  "get_weather",
				Input: map[string]interface{}{"location": "NYC"},
			},
		},
		Model:      "claude-sonnet-4-20250514",
		StopReason: strPtr("tool_use"),
		Usage: anthropic.Usage{
			InputTokens:  100,
			OutputTokens: 60,
		},
	}

	result := AnthropicToOpenAI(anthResp, "my-model")
	require.Len(t, result.Choices, 1)
	msg := result.Choices[0].Message
	require.Equal(t, "Let me check.", msg.Content)
	require.Len(t, msg.ToolCalls, 1)
	require.Equal(t, "toolu_123", msg.ToolCalls[0].ID)
	require.Equal(t, "function", msg.ToolCalls[0].Type)
	require.Equal(t, "get_weather", msg.ToolCalls[0].Function.Name)
	require.Contains(t, msg.ToolCalls[0].Function.Arguments, "NYC")
}

func TestAnthropicErrorToOpenAI(t *testing.T) {
	tests := []struct {
		name         string
		body         []byte
		expectedType string
		expectedMsg  string
	}{
		{
			name:         "valid anthropic error",
			body:         []byte(`{"type":"error","error":{"type":"rate_limit_error","message":"Too many requests"}}`),
			expectedType: "rate_limit_error",
			expectedMsg:  "Too many requests",
		},
		{
			name:         "invalid json wraps raw body",
			body:         []byte("upstream error text"),
			expectedType: "api_error",
			expectedMsg:  "upstream error text",
		},
		{
			name:         "overloaded maps to server_error",
			body:         []byte(`{"type":"error","error":{"type":"overloaded_error","message":"Server busy"}}`),
			expectedType: "server_error",
			expectedMsg:  "Server busy",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := AnthropicErrorToOpenAI(tc.body)
			require.NoError(t, err)

			var oaiErr OpenAIErrorResponse
			err = json.Unmarshal(result, &oaiErr)
			require.NoError(t, err)
			require.Equal(t, tc.expectedType, oaiErr.Error.Type)
			require.Equal(t, tc.expectedMsg, oaiErr.Error.Message)
		})
	}
}

// --- helpers ---

func strPtr(s string) *string { return &s }

func intPtr(i int) *int { return &i }
