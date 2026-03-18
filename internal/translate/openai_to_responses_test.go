// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"testing"

	"github.com/seckatie/glitchgate/internal/provider/openai"
	"github.com/stretchr/testify/require"
)

func float64Ptr(f float64) *float64 { return &f }

func TestOpenAIToResponses(t *testing.T) {
	tests := []struct {
		name    string
		req     *openai.ChatCompletionRequest
		model   string
		check   func(t *testing.T, resp *openai.ResponsesRequest)
		wantErr bool
	}{
		{
			name:    "NilRequest",
			req:     nil,
			model:   "gpt-4",
			wantErr: true,
		},
		{
			name: "StringContent",
			req: &openai.ChatCompletionRequest{
				Model: "gpt-4",
				Messages: []openai.ChatMessage{
					{Role: "user", Content: "Hello, world!"},
				},
			},
			model: "upstream-model",
			check: func(t *testing.T, resp *openai.ResponsesRequest) {
				require.Equal(t, "upstream-model", resp.Model)

				var items []openai.InputItem
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
			name: "SystemMessage",
			req: &openai.ChatCompletionRequest{
				Model: "gpt-4",
				Messages: []openai.ChatMessage{
					{Role: "system", Content: "You are helpful."},
					{Role: "user", Content: "Hi"},
				},
			},
			model: "upstream-model",
			check: func(t *testing.T, resp *openai.ResponsesRequest) {
				require.NotNil(t, resp.Instructions)
				require.Equal(t, "You are helpful.", *resp.Instructions)

				var items []openai.InputItem
				require.NoError(t, json.Unmarshal(resp.Input, &items))
				require.Len(t, items, 1)
				require.Equal(t, "user", items[0].Role)
			},
		},
		{
			name: "MaxTokens",
			req: &openai.ChatCompletionRequest{
				Model:     "gpt-4",
				MaxTokens: intPtr(256),
				Messages: []openai.ChatMessage{
					{Role: "user", Content: "Hi"},
				},
			},
			model: "upstream-model",
			check: func(t *testing.T, resp *openai.ResponsesRequest) {
				require.NotNil(t, resp.MaxOutputTokens)
				require.Equal(t, 256, *resp.MaxOutputTokens)
			},
		},
		{
			name: "AssistantMessage",
			req: &openai.ChatCompletionRequest{
				Model: "gpt-4",
				Messages: []openai.ChatMessage{
					{Role: "user", Content: "Hi"},
					{Role: "assistant", Content: "Hello there!"},
				},
			},
			model: "upstream-model",
			check: func(t *testing.T, resp *openai.ResponsesRequest) {
				var items []openai.InputItem
				require.NoError(t, json.Unmarshal(resp.Input, &items))
				require.Len(t, items, 2)

				require.Equal(t, "message", items[1].Type)
				require.Equal(t, "assistant", items[1].Role)

				var text string
				require.NoError(t, json.Unmarshal(items[1].Content, &text))
				require.Equal(t, "Hello there!", text)
			},
		},
		{
			name: "ToolCallsInAssistant",
			req: &openai.ChatCompletionRequest{
				Model: "gpt-4",
				Messages: []openai.ChatMessage{
					{Role: "user", Content: "What is the weather?"},
					{
						Role: "assistant",
						ToolCalls: []openai.ToolCall{
							{
								ID:   "call_1",
								Type: "function",
								Function: openai.FunctionCall{
									Name:      "get_weather",
									Arguments: `{"location":"NYC"}`,
								},
							},
							{
								ID:   "call_2",
								Type: "function",
								Function: openai.FunctionCall{
									Name:      "get_time",
									Arguments: `{"tz":"EST"}`,
								},
							},
						},
					},
				},
			},
			model: "upstream-model",
			check: func(t *testing.T, resp *openai.ResponsesRequest) {
				var items []openai.InputItem
				require.NoError(t, json.Unmarshal(resp.Input, &items))
				require.Len(t, items, 3) // 1 user + 2 function_calls

				require.Equal(t, "function_call", items[1].Type)
				require.Equal(t, "call_1", items[1].CallID)
				require.Equal(t, "get_weather", items[1].Name)
				require.Equal(t, `{"location":"NYC"}`, items[1].Arguments)

				require.Equal(t, "function_call", items[2].Type)
				require.Equal(t, "call_2", items[2].CallID)
				require.Equal(t, "get_time", items[2].Name)
				require.Equal(t, `{"tz":"EST"}`, items[2].Arguments)
			},
		},
		{
			name: "ToolMessage",
			req: &openai.ChatCompletionRequest{
				Model: "gpt-4",
				Messages: []openai.ChatMessage{
					{Role: "user", Content: "Weather?"},
					{
						Role:       "tool",
						Content:    "Sunny, 72F",
						ToolCallID: "call_abc",
					},
				},
			},
			model: "upstream-model",
			check: func(t *testing.T, resp *openai.ResponsesRequest) {
				var items []openai.InputItem
				require.NoError(t, json.Unmarshal(resp.Input, &items))
				require.Len(t, items, 2)

				require.Equal(t, "function_call_output", items[1].Type)
				require.Equal(t, "call_abc", items[1].CallID)
				require.Equal(t, "Sunny, 72F", items[1].Output)
			},
		},
		{
			name: "Tools",
			req: &openai.ChatCompletionRequest{
				Model: "gpt-4",
				Messages: []openai.ChatMessage{
					{Role: "user", Content: "Hi"},
				},
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: openai.ToolFunction{
							Name:        "get_weather",
							Description: "Get current weather",
							Parameters: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"location": map[string]interface{}{
										"type": "string",
									},
								},
							},
						},
					},
				},
			},
			model: "upstream-model",
			check: func(t *testing.T, resp *openai.ResponsesRequest) {
				require.Len(t, resp.Tools, 1)
				require.Equal(t, "function", resp.Tools[0].Type)
				require.Equal(t, "get_weather", resp.Tools[0].Name)
				require.Equal(t, "Get current weather", resp.Tools[0].Description)
				require.NotNil(t, resp.Tools[0].Parameters)

				var params map[string]interface{}
				require.NoError(t, json.Unmarshal(resp.Tools[0].Parameters, &params))
				require.Equal(t, "object", params["type"])
			},
		},
		{
			name: "ToolChoiceString",
			req: &openai.ChatCompletionRequest{
				Model: "gpt-4",
				Messages: []openai.ChatMessage{
					{Role: "user", Content: "Hi"},
				},
				ToolChoice: "auto",
			},
			model: "upstream-model",
			check: func(t *testing.T, resp *openai.ResponsesRequest) {
				require.Equal(t, "auto", resp.ToolChoice)
			},
		},
		{
			name: "ToolChoiceFunction",
			req: &openai.ChatCompletionRequest{
				Model: "gpt-4",
				Messages: []openai.ChatMessage{
					{Role: "user", Content: "Hi"},
				},
				ToolChoice: map[string]interface{}{
					"type": "function",
					"function": map[string]interface{}{
						"name": "get_weather",
					},
				},
			},
			model: "upstream-model",
			check: func(t *testing.T, resp *openai.ResponsesRequest) {
				tc, ok := resp.ToolChoice.(map[string]interface{})
				require.True(t, ok)
				require.Equal(t, "function", tc["type"])
				require.Equal(t, "get_weather", tc["name"])
			},
		},
		{
			name: "StreamFlag",
			req: &openai.ChatCompletionRequest{
				Model:  "gpt-4",
				Stream: true,
				Messages: []openai.ChatMessage{
					{Role: "user", Content: "Hi"},
				},
			},
			model: "upstream-model",
			check: func(t *testing.T, resp *openai.ResponsesRequest) {
				require.NotNil(t, resp.Stream)
				require.True(t, *resp.Stream)
			},
		},
		{
			name: "Temperature",
			req: &openai.ChatCompletionRequest{
				Model:       "gpt-4",
				Temperature: float64Ptr(0.7),
				Messages: []openai.ChatMessage{
					{Role: "user", Content: "Hi"},
				},
			},
			model: "upstream-model",
			check: func(t *testing.T, resp *openai.ResponsesRequest) {
				require.NotNil(t, resp.Temperature)
				require.InDelta(t, 0.7, *resp.Temperature, 0.001)
			},
		},
		{
			name: "MultipleSystemMessages",
			req: &openai.ChatCompletionRequest{
				Model: "gpt-4",
				Messages: []openai.ChatMessage{
					{Role: "system", Content: "You are helpful."},
					{Role: "system", Content: "Be concise."},
					{Role: "user", Content: "Hi"},
				},
			},
			model: "upstream-model",
			check: func(t *testing.T, resp *openai.ResponsesRequest) {
				require.NotNil(t, resp.Instructions)
				require.Equal(t, "You are helpful.\nBe concise.", *resp.Instructions)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := OpenAIToResponses(tt.req, tt.model)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, resp)
			if tt.check != nil {
				tt.check(t, resp)
			}
		})
	}
}

func TestOpenAIToResponses_PreservesAssistantTextAlongsideToolCalls(t *testing.T) {
	resp, err := OpenAIToResponses(&openai.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []openai.ChatMessage{
			{Role: "user", Content: "Compute this"},
			{
				Role:    "assistant",
				Content: "I'll check that.",
				ToolCalls: []openai.ToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: openai.FunctionCall{
							Name:      "lookup",
							Arguments: `{"id":1}`,
						},
					},
				},
			},
		},
	}, "upstream-model")
	require.NoError(t, err)

	var items []openai.InputItem
	require.NoError(t, json.Unmarshal(resp.Input, &items))
	require.Len(t, items, 3)

	require.Equal(t, "message", items[1].Type)
	require.Equal(t, "assistant", items[1].Role)

	var text string
	require.NoError(t, json.Unmarshal(items[1].Content, &text))
	require.Equal(t, "I'll check that.", text)

	require.Equal(t, "function_call", items[2].Type)
	require.Equal(t, "call_1", items[2].CallID)
}
