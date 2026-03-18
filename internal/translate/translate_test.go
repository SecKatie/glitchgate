package translate

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
	"github.com/seckatie/glitchgate/internal/provider/openai"
)

func TestOpenAIToAnthropic_BasicMessage(t *testing.T) {
	req := &openai.ChatCompletionRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []openai.ChatMessage{
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
		messages       []openai.ChatMessage
		expectedSystem string
		expectedCount  int // number of non-system messages
	}{
		{
			name: "single system message",
			messages: []openai.ChatMessage{
				{Role: "system", Content: "You are a helpful assistant."},
				{Role: "user", Content: "Hi"},
			},
			expectedSystem: "You are a helpful assistant.",
			expectedCount:  1,
		},
		{
			name: "multiple system messages concatenated",
			messages: []openai.ChatMessage{
				{Role: "system", Content: "You are a helpful assistant."},
				{Role: "system", Content: "Be concise."},
				{Role: "user", Content: "Hi"},
			},
			expectedSystem: "You are a helpful assistant.\nBe concise.",
			expectedCount:  1,
		},
		{
			name: "no system message",
			messages: []openai.ChatMessage{
				{Role: "user", Content: "Hi"},
			},
			expectedSystem: "",
			expectedCount:  1,
		},
		{
			name: "system between user messages",
			messages: []openai.ChatMessage{
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
			req := &openai.ChatCompletionRequest{
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
			req := &openai.ChatCompletionRequest{
				Model: "claude-sonnet-4-20250514",
				Messages: []openai.ChatMessage{
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

	req := &openai.ChatCompletionRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []openai.ChatMessage{
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
			req := &openai.ChatCompletionRequest{
				Model: "claude-sonnet-4-20250514",
				Messages: []openai.ChatMessage{
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
	req := &openai.ChatCompletionRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: "What is the weather?"},
			{
				Role:    "assistant",
				Content: nil,
				ToolCalls: []openai.ToolCall{
					{
						ID:   "call_123",
						Type: "function",
						Function: openai.FunctionCall{
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

			var oaiErr openai.OpenAIErrorResponse
			err = json.Unmarshal(result, &oaiErr)
			require.NoError(t, err)
			require.Equal(t, tc.expectedType, oaiErr.Error.Type)
			require.Equal(t, tc.expectedMsg, oaiErr.Error.Message)
		})
	}
}

func TestOpenAIToAnthropic_ReasoningEffort(t *testing.T) {
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
			maxTokens:    intPtr(5000),
			wantThinking: true,
			wantBudget:   4999,
		},
		{
			name:         "nil effort",
			effort:       nil,
			maxTokens:    intPtr(16384),
			wantThinking: false,
		},
		{
			name:         "empty effort",
			effort:       strPtr(""),
			maxTokens:    intPtr(16384),
			wantThinking: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &openai.ChatCompletionRequest{
				Model: "claude-sonnet-4-20250514",
				Messages: []openai.ChatMessage{
					{Role: "user", Content: "Think about this."},
				},
				MaxTokens:       tc.maxTokens,
				ReasoningEffort: tc.effort,
			}

			result, err := OpenAIToAnthropic(req)
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

func TestAnthropicToOpenAI_ThinkingBlocks(t *testing.T) {
	tests := []struct {
		name          string
		content       []anthropic.ContentBlock
		wantText      string
		wantReasoning string
	}{
		{
			name: "thinking block mapped to reasoning_content",
			content: []anthropic.ContentBlock{
				{Type: "thinking", Thinking: "Let me reason about this carefully step by step..."},
				{Type: "text", Text: "The answer is 42."},
			},
			wantText:      "The answer is 42.",
			wantReasoning: "Let me reason about this carefully step by step...",
		},
		{
			name: "no thinking blocks",
			content: []anthropic.ContentBlock{
				{Type: "text", Text: "Simple answer."},
			},
			wantText:      "Simple answer.",
			wantReasoning: "",
		},
		{
			name: "multiple thinking blocks concatenated",
			content: []anthropic.ContentBlock{
				{Type: "thinking", Thinking: "First I need to consider..."},
				{Type: "thinking", Thinking: "Then I should also think about..."},
				{Type: "text", Text: "Here is my response."},
			},
			wantText:      "Here is my response.",
			wantReasoning: "First I need to consider...\n\nThen I should also think about...",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			anthResp := &anthropic.MessagesResponse{
				ID:         "msg_thinking_test",
				Type:       "message",
				Role:       "assistant",
				Content:    tc.content,
				Model:      "claude-sonnet-4-20250514",
				StopReason: strPtr("end_turn"),
				Usage: anthropic.Usage{
					InputTokens:  100,
					OutputTokens: 50,
				},
			}

			result := AnthropicToOpenAI(anthResp, "test-model")
			require.NotNil(t, result)
			require.Len(t, result.Choices, 1)
			require.Equal(t, tc.wantText, result.Choices[0].Message.Content)
			require.Equal(t, tc.wantReasoning, result.Choices[0].Message.ReasoningContent)
			require.Nil(t, result.Usage.CompletionTokensDetails)
		})
	}
}

func TestAnthropicToOpenAIRequest_ToolResultBlocks(t *testing.T) {
	tests := []struct {
		name            string
		userBlocks      []anthropic.ContentBlock
		wantToolCallID  string
		wantToolContent string
	}{
		{
			name: "tool_result with string content",
			userBlocks: []anthropic.ContentBlock{
				{
					Type:      "tool_result",
					ToolUseID: "toolu_abc123",
					Content:   "72F and sunny",
				},
			},
			wantToolCallID:  "toolu_abc123",
			wantToolContent: "72F and sunny",
		},
		{
			name: "tool_result with array content blocks",
			userBlocks: []anthropic.ContentBlock{
				{
					Type:      "tool_result",
					ToolUseID: "toolu_def456",
					Content: []interface{}{
						map[string]interface{}{"type": "text", "text": "Result: "},
						map[string]interface{}{"type": "text", "text": "success"},
					},
				},
			},
			wantToolCallID:  "toolu_def456",
			wantToolContent: "Result: success",
		},
		{
			name: "tool_result with text fallback",
			userBlocks: []anthropic.ContentBlock{
				{
					Type:      "tool_result",
					ToolUseID: "toolu_ghi789",
					Text:      "fallback text",
				},
			},
			wantToolCallID:  "toolu_ghi789",
			wantToolContent: "fallback text",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			contentJSON, _ := json.Marshal(tc.userBlocks)
			var contentRaw interface{}
			_ = json.Unmarshal(contentJSON, &contentRaw)

			req := &anthropic.MessagesRequest{
				Model:     "claude-sonnet-4-20250514",
				MaxTokens: 1024,
				Messages: []anthropic.Message{
					{Role: "user", Content: "What's the weather?"},
					{
						Role: "assistant",
						Content: []anthropic.ContentBlock{
							{
								Type:  "tool_use",
								ID:    tc.userBlocks[0].ToolUseID,
								Name:  "get_weather",
								Input: map[string]interface{}{"location": "NYC"},
							},
						},
					},
					{Role: "user", Content: contentRaw},
				},
			}

			result, err := AnthropicToOpenAIRequest(req)
			require.NoError(t, err)
			require.NotNil(t, result)

			// Find the tool message in the output.
			var toolMsg *openai.ChatMessage
			for i := range result.Messages {
				if result.Messages[i].Role == "tool" {
					toolMsg = &result.Messages[i]
					break
				}
			}
			require.NotNil(t, toolMsg, "expected a tool message in output")
			require.Equal(t, tc.wantToolCallID, toolMsg.ToolCallID)
			require.Equal(t, tc.wantToolContent, toolMsg.Content)
		})
	}
}

func TestAnthropicToOpenAIRequest_AssistantToolUse(t *testing.T) {
	req := &anthropic.MessagesRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []anthropic.Message{
			{Role: "user", Content: "What's the weather?"},
			{
				Role: "assistant",
				Content: []anthropic.ContentBlock{
					{Type: "text", Text: "Let me check."},
					{
						Type:  "tool_use",
						ID:    "toolu_xyz",
						Name:  "get_weather",
						Input: map[string]interface{}{"location": "NYC"},
					},
				},
			},
		},
	}

	result, err := AnthropicToOpenAIRequest(req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Last message should be assistant with tool_calls.
	var assistantMsg *openai.ChatMessage
	for i := range result.Messages {
		if result.Messages[i].Role == "assistant" {
			assistantMsg = &result.Messages[i]
		}
	}
	require.NotNil(t, assistantMsg)
	require.Equal(t, "Let me check.", assistantMsg.Content)
	require.Len(t, assistantMsg.ToolCalls, 1)
	require.Equal(t, "toolu_xyz", assistantMsg.ToolCalls[0].ID)
	require.Equal(t, "function", assistantMsg.ToolCalls[0].Type)
	require.Equal(t, "get_weather", assistantMsg.ToolCalls[0].Function.Name)
	require.Contains(t, assistantMsg.ToolCalls[0].Function.Arguments, "NYC")
}

func TestAnthropicToOpenAIRequest_SystemMessage(t *testing.T) {
	req := &anthropic.MessagesRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		System:    "You are a helpful assistant.",
		Messages: []anthropic.Message{
			{Role: "user", Content: "Hi"},
		},
	}

	result, err := AnthropicToOpenAIRequest(req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.GreaterOrEqual(t, len(result.Messages), 2)
	require.Equal(t, "system", result.Messages[0].Role)
	require.Equal(t, "You are a helpful assistant.", result.Messages[0].Content)
}

func TestAnthropicToOpenAIRequest_SystemDropsBillingHeader(t *testing.T) {
	systemBlocks := []interface{}{
		map[string]interface{}{"type": "text", "text": "x-anthropic-billing-header: cc_version=2.1.77; cch=abc123;"},
		map[string]interface{}{"type": "text", "text": "You are a helpful assistant."},
		map[string]interface{}{"type": "text", "text": "Be concise."},
	}

	req := &anthropic.MessagesRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		System:    systemBlocks,
		Messages: []anthropic.Message{
			{Role: "user", Content: "Hi"},
		},
	}

	result, err := AnthropicToOpenAIRequest(req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "system", result.Messages[0].Role)
	require.Equal(t, "You are a helpful assistant.\nBe concise.", result.Messages[0].Content,
		"billing header block must be dropped to preserve upstream prefix caching")
}

// --- helpers ---

func strPtr(s string) *string { return &s }

func intPtr(i int) *int { return &i }

func TestOpenAIToAnthropic_ImageURL(t *testing.T) {
	req := &openai.ChatCompletionRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []openai.ChatMessage{
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": "data:image/jpeg;base64,/9j/abc123",
						},
					},
				},
			},
		},
	}

	result, err := OpenAIToAnthropic(req)
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)

	blocks, ok := result.Messages[0].Content.([]anthropic.ContentBlock)
	require.True(t, ok, "expected []ContentBlock")
	require.Len(t, blocks, 1)
	require.Equal(t, "image", blocks[0].Type)
	require.NotNil(t, blocks[0].Source)
	require.Equal(t, "base64", blocks[0].Source.Type)
	require.Equal(t, "image/jpeg", blocks[0].Source.MediaType)
	require.Equal(t, "/9j/abc123", blocks[0].Source.Data)
}

func TestOpenAIToAnthropic_ImageURLExternal(t *testing.T) {
	req := &openai.ChatCompletionRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []openai.ChatMessage{
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": "https://example.com/photo.jpg",
						},
					},
				},
			},
		},
	}

	result, err := OpenAIToAnthropic(req)
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)

	blocks, ok := result.Messages[0].Content.([]anthropic.ContentBlock)
	require.True(t, ok, "expected []ContentBlock")
	require.Len(t, blocks, 1)
	require.Equal(t, "image", blocks[0].Type)
	require.NotNil(t, blocks[0].Source)
	require.Equal(t, "url", blocks[0].Source.Type)
	require.Equal(t, "https://example.com/photo.jpg", blocks[0].Source.URL)
}

func TestOpenAIToAnthropic_MixedTextAndImage(t *testing.T) {
	req := &openai.ChatCompletionRequest{
		Model: "claude-sonnet-4-20250514",
		Messages: []openai.ChatMessage{
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "What is this?"},
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": "https://example.com/img.jpg",
						},
					},
				},
			},
		},
	}

	result, err := OpenAIToAnthropic(req)
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)

	blocks, ok := result.Messages[0].Content.([]anthropic.ContentBlock)
	require.True(t, ok, "expected []ContentBlock")
	require.Len(t, blocks, 2)
	require.Equal(t, "text", blocks[0].Type)
	require.Equal(t, "What is this?", blocks[0].Text)
	require.Equal(t, "image", blocks[1].Type)
	require.NotNil(t, blocks[1].Source)
	require.Equal(t, "url", blocks[1].Source.Type)
}

func TestAnthropicToOpenAIRequest_ImageBlock(t *testing.T) {
	userBlocks := []anthropic.ContentBlock{
		{
			Type: "image",
			Source: &anthropic.ImageSource{
				Type:      "base64",
				MediaType: "image/png",
				Data:      "iVBORw0KGgo=",
			},
		},
	}
	contentJSON, _ := json.Marshal(userBlocks)
	var contentRaw interface{}
	_ = json.Unmarshal(contentJSON, &contentRaw)

	req := &anthropic.MessagesRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []anthropic.Message{
			{Role: "user", Content: contentRaw},
		},
	}

	result, err := AnthropicToOpenAIRequest(req)
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)

	parts, ok := result.Messages[0].Content.([]openai.ContentPart)
	require.True(t, ok, "expected []openai.ContentPart")
	require.Len(t, parts, 1)
	require.Equal(t, "image_url", parts[0].Type)
	require.NotNil(t, parts[0].ImageURL)
	require.Equal(t, "data:image/png;base64,iVBORw0KGgo=", parts[0].ImageURL.URL)
}

func TestAnthropicToOpenAIRequest_ImageBlockURL(t *testing.T) {
	userBlocks := []anthropic.ContentBlock{
		{
			Type: "image",
			Source: &anthropic.ImageSource{
				Type: "url",
				URL:  "https://example.com/photo.jpg",
			},
		},
	}
	contentJSON, _ := json.Marshal(userBlocks)
	var contentRaw interface{}
	_ = json.Unmarshal(contentJSON, &contentRaw)

	req := &anthropic.MessagesRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []anthropic.Message{
			{Role: "user", Content: contentRaw},
		},
	}

	result, err := AnthropicToOpenAIRequest(req)
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)

	parts, ok := result.Messages[0].Content.([]openai.ContentPart)
	require.True(t, ok, "expected []openai.ContentPart")
	require.Len(t, parts, 1)
	require.Equal(t, "image_url", parts[0].Type)
	require.NotNil(t, parts[0].ImageURL)
	require.Equal(t, "https://example.com/photo.jpg", parts[0].ImageURL.URL)
}

func TestAnthropicToOpenAIRequest_ThinkingBlocksPreserved(t *testing.T) {
	// When an Anthropic client sends a multi-turn conversation that includes
	// a previous assistant response with thinking blocks, the thinking content
	// must be preserved as reasoning_content on the OpenAI openai.ChatMessage.
	req := &anthropic.MessagesRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []anthropic.Message{
			{Role: "user", Content: "Think step by step about why the sky is blue."},
			{
				Role: "assistant",
				Content: []anthropic.ContentBlock{
					{Type: "thinking", Thinking: "Rayleigh scattering causes shorter wavelengths..."},
					{Type: "text", Text: "The sky is blue because of Rayleigh scattering."},
				},
			},
			{Role: "user", Content: "Can you elaborate?"},
		},
	}

	result, err := AnthropicToOpenAIRequest(req)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Find the assistant message.
	var assistantMsg *openai.ChatMessage
	for i := range result.Messages {
		if result.Messages[i].Role == "assistant" {
			assistantMsg = &result.Messages[i]
			break
		}
	}
	require.NotNil(t, assistantMsg, "expected an assistant message")
	require.Equal(t, "The sky is blue because of Rayleigh scattering.", assistantMsg.Content)
	require.Equal(t, "Rayleigh scattering causes shorter wavelengths...", assistantMsg.ReasoningContent,
		"thinking block must be preserved as reasoning_content on subsequent turns")
}

func TestAnthropicToOpenAIRequest_MixedTextAndImage(t *testing.T) {
	userBlocks := []anthropic.ContentBlock{
		{Type: "text", Text: "Look at this:"},
		{
			Type: "image",
			Source: &anthropic.ImageSource{
				Type: "url",
				URL:  "https://example.com/img.jpg",
			},
		},
	}
	contentJSON, _ := json.Marshal(userBlocks)
	var contentRaw interface{}
	_ = json.Unmarshal(contentJSON, &contentRaw)

	req := &anthropic.MessagesRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []anthropic.Message{
			{Role: "user", Content: contentRaw},
		},
	}

	result, err := AnthropicToOpenAIRequest(req)
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)

	parts, ok := result.Messages[0].Content.([]openai.ContentPart)
	require.True(t, ok, "expected []openai.ContentPart")
	require.Len(t, parts, 2)
	require.Equal(t, "text", parts[0].Type)
	require.Equal(t, "Look at this:", parts[0].Text)
	require.Equal(t, "image_url", parts[1].Type)
	require.NotNil(t, parts[1].ImageURL)
	require.Equal(t, "https://example.com/img.jpg", parts[1].ImageURL.URL)
}
