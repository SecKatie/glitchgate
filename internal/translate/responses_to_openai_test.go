// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"testing"

	"github.com/seckatie/glitchgate/internal/provider/openai"
	"github.com/stretchr/testify/require"
)

func TestResponsesToOpenAI_NilRequest(t *testing.T) {
	_, err := ResponsesToOpenAI(nil, "gpt-4o")
	require.Error(t, err)
	require.Contains(t, err.Error(), "must not be nil")
}

func TestResponsesToOpenAI_StringInput(t *testing.T) {
	input, _ := json.Marshal("Hello!")
	req := &openai.ResponsesRequest{
		Model: "test-model",
		Input: input,
	}

	result, err := ResponsesToOpenAI(req, "gpt-4o")
	require.NoError(t, err)
	require.Equal(t, "gpt-4o", result.Model)
	require.Len(t, result.Messages, 1)
	require.Equal(t, "user", result.Messages[0].Role)
	require.Equal(t, "Hello!", result.Messages[0].Content)
}

func TestResponsesToOpenAI_Instructions(t *testing.T) {
	input, _ := json.Marshal("Hi")
	instructions := "You are a helpful assistant."
	req := &openai.ResponsesRequest{
		Model:        "test-model",
		Input:        input,
		Instructions: &instructions,
	}

	result, err := ResponsesToOpenAI(req, "gpt-4o")
	require.NoError(t, err)
	// Instructions should become a system message.
	require.GreaterOrEqual(t, len(result.Messages), 2)
	require.Equal(t, "system", result.Messages[0].Role)
	require.Equal(t, "You are a helpful assistant.", result.Messages[0].Content)
	require.Equal(t, "user", result.Messages[1].Role)
}

func TestResponsesToOpenAI_MaxTokens(t *testing.T) {
	input, _ := json.Marshal("Hi")
	maxTokens := 500
	req := &openai.ResponsesRequest{
		Model:           "test-model",
		Input:           input,
		MaxOutputTokens: &maxTokens,
	}

	result, err := ResponsesToOpenAI(req, "gpt-4o")
	require.NoError(t, err)
	require.NotNil(t, result.MaxTokens)
	require.Equal(t, 500, *result.MaxTokens)
}

func TestResponsesToOpenAI_MessageInput(t *testing.T) {
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

	result, err := ResponsesToOpenAI(req, "gpt-4o")
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)
	require.Equal(t, "user", result.Messages[0].Role)
}

func TestResponsesToOpenAI_FunctionCallInput(t *testing.T) {
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

	result, err := ResponsesToOpenAI(req, "gpt-4o")
	require.NoError(t, err)
	require.Len(t, result.Messages, 3)

	// User message.
	require.Equal(t, "user", result.Messages[0].Role)

	// Function call → assistant with tool_calls.
	require.Equal(t, "assistant", result.Messages[1].Role)
	require.Len(t, result.Messages[1].ToolCalls, 1)
	require.Equal(t, "call_123", result.Messages[1].ToolCalls[0].ID)
	require.Equal(t, "get_weather", result.Messages[1].ToolCalls[0].Function.Name)

	// Function call output → tool message.
	require.Equal(t, "tool", result.Messages[2].Role)
	require.Equal(t, "call_123", result.Messages[2].ToolCallID)
}

func TestResponsesToOpenAI_Tools(t *testing.T) {
	params := json.RawMessage(`{"type":"object","properties":{"location":{"type":"string"}}}`)
	input, _ := json.Marshal("Hi")
	req := &openai.ResponsesRequest{
		Model: "test-model",
		Input: input,
		Tools: []openai.ResponsesTool{
			{Type: "function", Name: "get_weather", Description: "Get weather", Parameters: params},
		},
	}

	result, err := ResponsesToOpenAI(req, "gpt-4o")
	require.NoError(t, err)
	require.Len(t, result.Tools, 1)
	require.Equal(t, "function", result.Tools[0].Type)
	require.Equal(t, "get_weather", result.Tools[0].Function.Name)
	require.Equal(t, "Get weather", result.Tools[0].Function.Description)
}

func TestResponsesToOpenAI_UnsupportedToolType(t *testing.T) {
	input, _ := json.Marshal("Hi")
	req := &openai.ResponsesRequest{
		Model: "test-model",
		Input: input,
		Tools: []openai.ResponsesTool{
			{Type: "code_interpreter", Name: "code_interpreter"},
		},
	}

	_, err := ResponsesToOpenAI(req, "gpt-4o")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not supported for Chat Completions upstream")
}

func TestResponsesToOpenAI_ToolChoice(t *testing.T) {
	tests := []struct {
		name       string
		toolChoice interface{}
		expected   interface{}
	}{
		{
			name:       "auto string",
			toolChoice: "auto",
			expected:   "auto",
		},
		{
			name:       "none string",
			toolChoice: "none",
			expected:   "none",
		},
		{
			name:       "required string",
			toolChoice: "required",
			expected:   "required",
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

			result, err := ResponsesToOpenAI(req, "gpt-4o")
			require.NoError(t, err)
			require.Equal(t, tc.expected, result.ToolChoice)
		})
	}
}

func TestResponsesToOpenAI_Streaming(t *testing.T) {
	streaming := true
	input, _ := json.Marshal("Hi")
	req := &openai.ResponsesRequest{
		Model:  "test-model",
		Input:  input,
		Stream: &streaming,
	}

	result, err := ResponsesToOpenAI(req, "gpt-4o")
	require.NoError(t, err)
	require.True(t, result.Stream)
	require.NotNil(t, result.StreamOptions)
	require.True(t, result.StreamOptions.IncludeUsage)
}

func TestResponsesToOpenAI_InputFileError(t *testing.T) {
	items := []openai.InputItem{
		{Type: "input_file", FileData: "base64data"},
	}
	input, _ := json.Marshal(items)
	req := &openai.ResponsesRequest{
		Model: "test-model",
		Input: input,
	}

	_, err := ResponsesToOpenAI(req, "gpt-4o")
	require.Error(t, err)
	require.Contains(t, err.Error(), "input_file")
}

func TestResponsesToOpenAI_InputAudioPassthrough(t *testing.T) {
	items := []openai.InputItem{
		{Type: "input_audio", Data: "base64audio", Format: "wav"},
	}
	input, _ := json.Marshal(items)
	req := &openai.ResponsesRequest{
		Model: "test-model",
		Input: input,
	}

	result, err := ResponsesToOpenAI(req, "gpt-4o")
	require.NoError(t, err)
	// input_audio should pass through to CC (some providers support it).
	require.Len(t, result.Messages, 1)
	require.Equal(t, "user", result.Messages[0].Role)
}

func TestResponsesToOpenAI_Temperature(t *testing.T) {
	temp := 0.7
	topP := 0.9
	input, _ := json.Marshal("Hi")
	req := &openai.ResponsesRequest{
		Model:       "test-model",
		Input:       input,
		Temperature: &temp,
		TopP:        &topP,
	}

	result, err := ResponsesToOpenAI(req, "gpt-4o")
	require.NoError(t, err)
	require.NotNil(t, result.Temperature)
	require.InDelta(t, 0.7, *result.Temperature, 0.001)
	require.NotNil(t, result.TopP)
	require.InDelta(t, 0.9, *result.TopP, 0.001)
}

func TestOpenAIToResponsesResponse_TextOnly(t *testing.T) {
	body := `{
		"id": "chatcmpl-abc123",
		"object": "chat.completion",
		"created": 1710288000,
		"model": "gpt-4o",
		"choices": [{"index": 0, "message": {"role": "assistant", "content": "Hello!"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`

	resp := OpenAIToResponsesResponse([]byte(body), "test-model")
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

func TestOpenAIToResponsesResponse_ToolCalls(t *testing.T) {
	body := `{
		"id": "chatcmpl-def456",
		"object": "chat.completion",
		"created": 1710288000,
		"model": "gpt-4o",
		"choices": [{"index": 0, "message": {"role": "assistant", "content": "", "tool_calls": [
			{"id": "call_1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"location\":\"SF\"}"}}
		]}, "finish_reason": "tool_calls"}],
		"usage": {"prompt_tokens": 20, "completion_tokens": 15, "total_tokens": 35}
	}`

	resp := OpenAIToResponsesResponse([]byte(body), "test-model")
	require.Equal(t, "completed", resp.Status)
	require.Len(t, resp.Output, 1) // No text content, just function call.
	require.Equal(t, "function_call", resp.Output[0].Type)
	require.Equal(t, "get_weather", resp.Output[0].Name)
	require.Equal(t, "call_1", resp.Output[0].CallID)
}

func TestOpenAIToResponsesResponse_Refusal(t *testing.T) {
	body := `{
		"id": "chatcmpl-xyz",
		"object": "chat.completion",
		"created": 1710288000,
		"choices": [{"index": 0, "message": {"role": "assistant", "content": "", "refusal": "I cannot help with that."}, "finish_reason": "stop"}]
	}`

	resp := OpenAIToResponsesResponse([]byte(body), "test-model")
	require.Len(t, resp.Output, 1)
	require.Equal(t, "message", resp.Output[0].Type)
	require.Len(t, resp.Output[0].Content, 1)
	require.Equal(t, "refusal", resp.Output[0].Content[0].Type)
	require.Equal(t, "I cannot help with that.", resp.Output[0].Content[0].Refusal)
}

func TestOpenAIToResponsesResponse_LengthFinishReason(t *testing.T) {
	body := `{
		"id": "chatcmpl-len",
		"object": "chat.completion",
		"created": 1710288000,
		"choices": [{"index": 0, "message": {"role": "assistant", "content": "partial..."}, "finish_reason": "length"}]
	}`

	resp := OpenAIToResponsesResponse([]byte(body), "test-model")
	require.Equal(t, "incomplete", resp.Status)
}

func TestOpenAIToResponsesResponse_InvalidJSON(t *testing.T) {
	resp := OpenAIToResponsesResponse([]byte("bad json"), "test-model")
	require.Equal(t, "failed", resp.Status)
	require.NotNil(t, resp.Error)
	require.Equal(t, "server_error", resp.Error.Code)
}

func TestOpenAIToResponsesResponse_NoChoices(t *testing.T) {
	body := `{
		"id": "chatcmpl-empty",
		"object": "chat.completion",
		"created": 1710288000,
		"choices": []
	}`

	resp := OpenAIToResponsesResponse([]byte(body), "test-model")
	require.Equal(t, "completed", resp.Status)
	require.Empty(t, resp.Output)
}

func TestResponsesToOpenAI_ReasoningEffort(t *testing.T) {
	input, _ := json.Marshal("Think hard about this.")
	effort := "high"
	req := &openai.ResponsesRequest{
		Model: "test-model",
		Input: input,
		Reasoning: &openai.Reasoning{
			Effort: &effort,
		},
	}

	result, err := ResponsesToOpenAI(req, "gpt-4o")
	require.NoError(t, err)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "high", *result.ReasoningEffort)
}

func TestResponsesToOpenAI_NoReasoning(t *testing.T) {
	input, _ := json.Marshal("Hi")
	req := &openai.ResponsesRequest{
		Model: "test-model",
		Input: input,
	}

	result, err := ResponsesToOpenAI(req, "gpt-4o")
	require.NoError(t, err)
	require.Nil(t, result.ReasoningEffort)
}

func TestOpenAIToResponsesResponse_ReasoningTokens(t *testing.T) {
	body := `{
		"id": "chatcmpl-reason",
		"object": "chat.completion",
		"created": 1710288000,
		"model": "o3-mini",
		"choices": [{"index": 0, "message": {"role": "assistant", "content": "Answer."}, "finish_reason": "stop"}],
		"usage": {
			"prompt_tokens": 100,
			"completion_tokens": 50,
			"total_tokens": 150,
			"completion_tokens_details": {"reasoning_tokens": 30}
		}
	}`

	resp := OpenAIToResponsesResponse([]byte(body), "test-model")
	require.NotNil(t, resp.Usage)
	require.NotNil(t, resp.Usage.OutputTokensDetails)
	require.Equal(t, int64(30), resp.Usage.OutputTokensDetails.ReasoningTokens)
}

func TestOpenAIToResponsesResponse_NoReasoningTokens(t *testing.T) {
	body := `{
		"id": "chatcmpl-noreason",
		"object": "chat.completion",
		"created": 1710288000,
		"model": "gpt-4o",
		"choices": [{"index": 0, "message": {"role": "assistant", "content": "Hi."}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`

	resp := OpenAIToResponsesResponse([]byte(body), "test-model")
	require.NotNil(t, resp.Usage)
	require.Nil(t, resp.Usage.OutputTokensDetails)
}

func TestResponsesToOpenAIResponse_ReasoningTokensForwarded(t *testing.T) {
	body := `{
		"id": "resp_reason",
		"object": "response",
		"status": "completed",
		"output": [
			{"type": "message", "id": "msg_1", "role": "assistant", "content": [{"type": "output_text", "text": "Answer."}]}
		],
		"usage": {
			"input_tokens": 100,
			"output_tokens": 50,
			"total_tokens": 150,
			"output_tokens_details": {"reasoning_tokens": 25}
		}
	}`

	ccResp := ResponsesToOpenAIResponse([]byte(body), "test-model")
	require.NotNil(t, ccResp.Usage)
	require.NotNil(t, ccResp.Usage.CompletionTokensDetails)
	require.Equal(t, int64(25), ccResp.Usage.CompletionTokensDetails.ReasoningTokens)
}

func TestResponsesToOpenAIResponse_NoReasoningTokens(t *testing.T) {
	body := `{
		"id": "resp_noreason",
		"object": "response",
		"status": "completed",
		"output": [
			{"type": "message", "id": "msg_1", "role": "assistant", "content": [{"type": "output_text", "text": "Hi."}]}
		],
		"usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}
	}`

	ccResp := ResponsesToOpenAIResponse([]byte(body), "test-model")
	require.NotNil(t, ccResp.Usage)
	require.Nil(t, ccResp.Usage.CompletionTokensDetails)
}

func TestOpenAIToResponsesResponse_CachedTokens(t *testing.T) {
	body := `{
		"id": "chatcmpl-cached",
		"object": "chat.completion",
		"created": 1710288000,
		"model": "gpt-4o",
		"choices": [{"index": 0, "message": {"role": "assistant", "content": "Hi."}, "finish_reason": "stop"}],
		"usage": {
			"prompt_tokens": 100,
			"completion_tokens": 20,
			"total_tokens": 120,
			"prompt_tokens_details": {"cached_tokens": 80}
		}
	}`

	resp := OpenAIToResponsesResponse([]byte(body), "test-model")
	require.NotNil(t, resp.Usage)
	require.NotNil(t, resp.Usage.InputTokensDetails)
	require.Equal(t, int64(80), resp.Usage.InputTokensDetails.CachedTokens)
}

func TestOpenAIToResponsesResponse_NoCachedTokens(t *testing.T) {
	body := `{
		"id": "chatcmpl-nocache",
		"object": "chat.completion",
		"created": 1710288000,
		"model": "gpt-4o",
		"choices": [{"index": 0, "message": {"role": "assistant", "content": "Hi."}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`

	resp := OpenAIToResponsesResponse([]byte(body), "test-model")
	require.NotNil(t, resp.Usage)
	require.Nil(t, resp.Usage.InputTokensDetails)
}

func TestResponsesToOpenAIResponse_CachedTokens(t *testing.T) {
	body := `{
		"id": "resp_cached",
		"object": "response",
		"status": "completed",
		"output": [
			{"type": "message", "id": "msg_1", "role": "assistant", "content": [{"type": "output_text", "text": "Hi."}]}
		],
		"usage": {
			"input_tokens": 100,
			"output_tokens": 20,
			"total_tokens": 120,
			"input_tokens_details": {"cached_tokens": 80}
		}
	}`

	ccResp := ResponsesToOpenAIResponse([]byte(body), "test-model")
	require.NotNil(t, ccResp.Usage)
	require.NotNil(t, ccResp.Usage.PromptTokensDetails)
	require.Equal(t, int64(80), ccResp.Usage.PromptTokensDetails.CachedTokens)
}

func TestResponsesToOpenAIResponse_NoCachedTokens(t *testing.T) {
	body := `{
		"id": "resp_nocache",
		"object": "response",
		"status": "completed",
		"output": [
			{"type": "message", "id": "msg_1", "role": "assistant", "content": [{"type": "output_text", "text": "Hi."}]}
		],
		"usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}
	}`

	ccResp := ResponsesToOpenAIResponse([]byte(body), "test-model")
	require.NotNil(t, ccResp.Usage)
	require.Nil(t, ccResp.Usage.PromptTokensDetails)
}
