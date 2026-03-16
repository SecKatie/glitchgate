// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"testing"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
	"github.com/stretchr/testify/require"
)

func TestAnthropicToResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     *anthropic.MessagesRequest
		model   string
		check   func(t *testing.T, resp *ResponsesRequest)
		wantErr bool
	}{
		{
			name:    "NilRequest",
			req:     nil,
			model:   "gpt-4o",
			wantErr: true,
		},
		{
			name: "StringContent",
			req: &anthropic.MessagesRequest{
				Model:     "claude-3-opus",
				MaxTokens: 1024,
				Messages: []anthropic.Message{
					{Role: "user", Content: "Hello, world!"},
				},
			},
			model: "gpt-4o",
			check: func(t *testing.T, resp *ResponsesRequest) {
				require.Equal(t, "gpt-4o", resp.Model)

				var items []InputItem
				require.NoError(t, json.Unmarshal(resp.Input, &items))
				require.Len(t, items, 1)
				require.Equal(t, "message", items[0].Type)
				require.Equal(t, "user", items[0].Role)

				var text string
				require.NoError(t, json.Unmarshal(items[0].Content, &text))
				require.Equal(t, "Hello, world!", text)
			},
		},
		{
			name: "SystemString",
			req: &anthropic.MessagesRequest{
				Model:     "claude-3-opus",
				MaxTokens: 1024,
				System:    "You are a helpful assistant.",
				Messages: []anthropic.Message{
					{Role: "user", Content: "Hi"},
				},
			},
			model: "gpt-4o",
			check: func(t *testing.T, resp *ResponsesRequest) {
				require.NotNil(t, resp.Instructions)
				require.Equal(t, "You are a helpful assistant.", *resp.Instructions)
			},
		},
		{
			name: "MaxTokens",
			req: &anthropic.MessagesRequest{
				Model:     "claude-3-opus",
				MaxTokens: 2048,
				Messages: []anthropic.Message{
					{Role: "user", Content: "Hi"},
				},
			},
			model: "gpt-4o",
			check: func(t *testing.T, resp *ResponsesRequest) {
				require.NotNil(t, resp.MaxOutputTokens)
				require.Equal(t, 2048, *resp.MaxOutputTokens)
			},
		},
		{
			name: "ContentBlockArray",
			req: &anthropic.MessagesRequest{
				Model:     "claude-3-opus",
				MaxTokens: 1024,
				Messages: []anthropic.Message{
					{
						Role: "user",
						Content: []anthropic.ContentBlock{
							{Type: "text", Text: "First paragraph."},
							{Type: "text", Text: "Second paragraph."},
						},
					},
				},
			},
			model: "gpt-4o",
			check: func(t *testing.T, resp *ResponsesRequest) {
				var items []InputItem
				require.NoError(t, json.Unmarshal(resp.Input, &items))
				require.Len(t, items, 2)

				for _, item := range items {
					require.Equal(t, "message", item.Type)
					require.Equal(t, "user", item.Role)
				}
			},
		},
		{
			name: "ToolUseBlock",
			req: &anthropic.MessagesRequest{
				Model:     "claude-3-opus",
				MaxTokens: 1024,
				Messages: []anthropic.Message{
					{
						Role: "assistant",
						Content: []anthropic.ContentBlock{
							{
								Type:  "tool_use",
								ID:    "call_123",
								Name:  "get_weather",
								Input: map[string]interface{}{"city": "Portland"},
							},
						},
					},
				},
			},
			model: "gpt-4o",
			check: func(t *testing.T, resp *ResponsesRequest) {
				var items []InputItem
				require.NoError(t, json.Unmarshal(resp.Input, &items))
				require.Len(t, items, 1)
				require.Equal(t, "function_call", items[0].Type)
				require.Equal(t, "call_123", items[0].CallID)
				require.Equal(t, "get_weather", items[0].Name)

				var args map[string]interface{}
				require.NoError(t, json.Unmarshal([]byte(items[0].Arguments), &args))
				require.Equal(t, "Portland", args["city"])
			},
		},
		{
			name: "ToolResultBlock",
			req: &anthropic.MessagesRequest{
				Model:     "claude-3-opus",
				MaxTokens: 1024,
				Messages: []anthropic.Message{
					{
						Role: "user",
						Content: []anthropic.ContentBlock{
							{
								Type: "tool_result",
								ID:   "call_123",
								Text: "72°F and sunny",
							},
						},
					},
				},
			},
			model: "gpt-4o",
			check: func(t *testing.T, resp *ResponsesRequest) {
				var items []InputItem
				require.NoError(t, json.Unmarshal(resp.Input, &items))
				require.Len(t, items, 1)
				require.Equal(t, "function_call_output", items[0].Type)
				require.Equal(t, "call_123", items[0].CallID)
				require.Equal(t, "72°F and sunny", items[0].Output)
			},
		},
		{
			name: "Tools",
			req: &anthropic.MessagesRequest{
				Model:     "claude-3-opus",
				MaxTokens: 1024,
				Messages: []anthropic.Message{
					{Role: "user", Content: "Hi"},
				},
				Tools: []anthropic.Tool{
					{
						Name:        "get_weather",
						Description: "Get the weather",
						InputSchema: map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"city": map[string]interface{}{"type": "string"},
							},
						},
					},
				},
			},
			model: "gpt-4o",
			check: func(t *testing.T, resp *ResponsesRequest) {
				require.Len(t, resp.Tools, 1)
				require.Equal(t, "function", resp.Tools[0].Type)
				require.Equal(t, "get_weather", resp.Tools[0].Name)
				require.Equal(t, "Get the weather", resp.Tools[0].Description)
				require.NotNil(t, resp.Tools[0].Parameters)

				var params map[string]interface{}
				require.NoError(t, json.Unmarshal(resp.Tools[0].Parameters, &params))
				require.Equal(t, "object", params["type"])
			},
		},
		{
			name: "ToolChoiceAuto",
			req: &anthropic.MessagesRequest{
				Model:     "claude-3-opus",
				MaxTokens: 1024,
				Messages: []anthropic.Message{
					{Role: "user", Content: "Hi"},
				},
				ToolChoice: map[string]interface{}{"type": "auto"},
			},
			model: "gpt-4o",
			check: func(t *testing.T, resp *ResponsesRequest) {
				require.Equal(t, "auto", resp.ToolChoice)
			},
		},
		{
			name: "ToolChoiceAny",
			req: &anthropic.MessagesRequest{
				Model:     "claude-3-opus",
				MaxTokens: 1024,
				Messages: []anthropic.Message{
					{Role: "user", Content: "Hi"},
				},
				ToolChoice: map[string]interface{}{"type": "any"},
			},
			model: "gpt-4o",
			check: func(t *testing.T, resp *ResponsesRequest) {
				require.Equal(t, "required", resp.ToolChoice)
			},
		},
		{
			name: "ToolChoiceTool",
			req: &anthropic.MessagesRequest{
				Model:     "claude-3-opus",
				MaxTokens: 1024,
				Messages: []anthropic.Message{
					{Role: "user", Content: "Hi"},
				},
				ToolChoice: map[string]interface{}{"type": "tool", "name": "get_weather"},
			},
			model: "gpt-4o",
			check: func(t *testing.T, resp *ResponsesRequest) {
				tc, ok := resp.ToolChoice.(map[string]interface{})
				require.True(t, ok)
				require.Equal(t, "function", tc["type"])
				require.Equal(t, "get_weather", tc["name"])
			},
		},
		{
			name: "StreamFlag",
			req: &anthropic.MessagesRequest{
				Model:     "claude-3-opus",
				MaxTokens: 1024,
				Stream:    true,
				Messages: []anthropic.Message{
					{Role: "user", Content: "Hi"},
				},
			},
			model: "gpt-4o",
			check: func(t *testing.T, resp *ResponsesRequest) {
				require.NotNil(t, resp.Stream)
				require.True(t, *resp.Stream)
			},
		},
		{
			name: "Temperature",
			req: &anthropic.MessagesRequest{
				Model:       "claude-3-opus",
				MaxTokens:   1024,
				Temperature: floatPtr(0.7),
				Messages: []anthropic.Message{
					{Role: "user", Content: "Hi"},
				},
			},
			model: "gpt-4o",
			check: func(t *testing.T, resp *ResponsesRequest) {
				require.NotNil(t, resp.Temperature)
				require.InDelta(t, 0.7, *resp.Temperature, 0.001)
			},
		},
		{
			name: "ThinkingToReasoning",
			req: &anthropic.MessagesRequest{
				Model:     "claude-3-opus",
				MaxTokens: 16384,
				Messages: []anthropic.Message{
					{Role: "user", Content: "Think about this."},
				},
				Thinking: &anthropic.ThinkingConfig{
					Type:         "enabled",
					BudgetTokens: 10000,
				},
			},
			model: "gpt-4o",
			check: func(t *testing.T, resp *ResponsesRequest) {
				require.NotNil(t, resp.Reasoning)
				require.NotNil(t, resp.Reasoning.Effort)
				require.Equal(t, "high", *resp.Reasoning.Effort)
				require.NotNil(t, resp.Reasoning.Summary)
				require.Equal(t, "auto", *resp.Reasoning.Summary)
			},
		},
		{
			name: "ThinkingLowBudget",
			req: &anthropic.MessagesRequest{
				Model:     "claude-3-opus",
				MaxTokens: 16384,
				Messages: []anthropic.Message{
					{Role: "user", Content: "Quick question."},
				},
				Thinking: &anthropic.ThinkingConfig{
					Type:         "enabled",
					BudgetTokens: 1024,
				},
			},
			model: "gpt-4o",
			check: func(t *testing.T, resp *ResponsesRequest) {
				require.NotNil(t, resp.Reasoning)
				require.NotNil(t, resp.Reasoning.Effort)
				require.Equal(t, "low", *resp.Reasoning.Effort)
			},
		},
		{
			name: "ThinkingMediumBudget",
			req: &anthropic.MessagesRequest{
				Model:     "claude-3-opus",
				MaxTokens: 16384,
				Messages: []anthropic.Message{
					{Role: "user", Content: "Moderate question."},
				},
				Thinking: &anthropic.ThinkingConfig{
					Type:         "enabled",
					BudgetTokens: 5000,
				},
			},
			model: "gpt-4o",
			check: func(t *testing.T, resp *ResponsesRequest) {
				require.NotNil(t, resp.Reasoning)
				require.NotNil(t, resp.Reasoning.Effort)
				require.Equal(t, "medium", *resp.Reasoning.Effort)
			},
		},
		{
			name: "NoThinkingNoReasoning",
			req: &anthropic.MessagesRequest{
				Model:     "claude-3-opus",
				MaxTokens: 1024,
				Messages: []anthropic.Message{
					{Role: "user", Content: "Hi"},
				},
			},
			model: "gpt-4o",
			check: func(t *testing.T, resp *ResponsesRequest) {
				require.Nil(t, resp.Reasoning)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp, err := AnthropicToResponses(tt.req, tt.model)
			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, resp)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, resp)
			tt.check(t, resp)
		})
	}
}

func floatPtr(f float64) *float64 {
	return &f
}
