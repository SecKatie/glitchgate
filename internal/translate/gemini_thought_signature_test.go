// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	anthropic "github.com/seckatie/glitchgate/internal/provider/anthropic"
)

func TestEncodeGeminiToolCallIDRoundTrip(t *testing.T) {
	t.Parallel()

	encoded := encodeGeminiToolCallID("call_123", "sig+/=")
	baseID, thoughtSignature := decodeGeminiToolCallID(encoded)

	require.Equal(t, "call_123", baseID)
	require.Equal(t, "sig+/=", thoughtSignature)
}

func TestOpenAIGeminiRoundTripPreservesThoughtSignature(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"candidates":[
			{
				"content":{
					"role":"model",
					"parts":[
						{
							"functionCall":{"name":"default_api:Read","args":{"file_path":"README.md"}},
							"thoughtSignature":"sig-openai"
						}
					]
				},
				"finishReason":"STOP"
			}
		]
	}`)

	ccResp := GeminiToOpenAIResponse(body, "gemini-2.5-pro")
	require.Len(t, ccResp.Choices, 1)
	require.Len(t, ccResp.Choices[0].Message.ToolCalls, 1)

	toolCall := ccResp.Choices[0].Message.ToolCalls[0]
	require.NotEqual(t, "call_000000", toolCall.ID)

	req := &ChatCompletionRequest{
		Model: "gemini-2.5-pro",
		Messages: []ChatMessage{
			{Role: "assistant", ToolCalls: []ToolCall{toolCall}},
			{Role: "tool", ToolCallID: toolCall.ID, Content: `{"ok":true}`},
		},
	}

	gemReq, err := OpenAIToGeminiRequest(req)
	require.NoError(t, err)
	require.Len(t, gemReq.Contents, 2)
	require.Len(t, gemReq.Contents[0].Parts, 1)
	require.Equal(t, "sig-openai", gemReq.Contents[0].Parts[0].ThoughtSignature)
	require.Equal(t, "default_api:Read", gemReq.Contents[1].Parts[0].FunctionResponse.Name)
}

func TestAnthropicGeminiRoundTripPreservesThoughtSignature(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"candidates":[
			{
				"content":{
					"role":"model",
					"parts":[
						{
							"functionCall":{"name":"default_api:Read","args":{"file_path":"README.md"}},
							"thoughtSignature":"sig-anthropic"
						}
					]
				},
				"finishReason":"STOP"
			}
		]
	}`)

	anthResp := GeminiToAnthropicResponse(body, "claude-sonnet")
	require.Len(t, anthResp.Content, 1)

	req := &anthropic.MessagesRequest{
		Model: "gemini-2.5-pro",
		Messages: []anthropic.Message{
			{
				Role: "assistant",
				Content: []interface{}{
					map[string]interface{}{
						"type":  "tool_use",
						"id":    anthResp.Content[0].ID,
						"name":  anthResp.Content[0].Name,
						"input": anthResp.Content[0].Input,
					},
				},
			},
			{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{
						"type":        "tool_result",
						"tool_use_id": anthResp.Content[0].ID,
						"content":     "done",
					},
				},
			},
		},
	}

	gemReq, err := AnthropicToGeminiRequest(req)
	require.NoError(t, err)
	require.Len(t, gemReq.Contents, 2)
	require.Equal(t, "sig-anthropic", gemReq.Contents[0].Parts[0].ThoughtSignature)
	require.Equal(t, "default_api:Read", gemReq.Contents[1].Parts[0].FunctionResponse.Name)
}

func TestResponsesGeminiRoundTripPreservesThoughtSignature(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"candidates":[
			{
				"content":{
					"role":"model",
					"parts":[
						{
							"functionCall":{"name":"default_api:Read","args":{"file_path":"README.md"}},
							"thoughtSignature":"sig-responses"
						}
					]
				},
				"finishReason":"STOP"
			}
		]
	}`)

	respResp := GeminiToResponsesResponse(body, "gemini-2.5-pro")
	require.Len(t, respResp.Output, 1)

	input, err := json.Marshal([]InputItem{
		{
			Type:      "function_call",
			CallID:    respResp.Output[0].CallID,
			Name:      respResp.Output[0].Name,
			Arguments: respResp.Output[0].Arguments,
		},
		{
			Type:   "function_call_output",
			CallID: respResp.Output[0].CallID,
			Output: `{"ok":true}`,
		},
	})
	require.NoError(t, err)

	req := &ResponsesRequest{
		Model: "gemini-2.5-pro",
		Input: input,
	}

	gemReq, err := ResponsesToGeminiRequest(req)
	require.NoError(t, err)
	require.Len(t, gemReq.Contents, 2)
	require.Equal(t, "sig-responses", gemReq.Contents[0].Parts[0].ThoughtSignature)
	require.Equal(t, "default_api:Read", gemReq.Contents[1].Parts[0].FunctionResponse.Name)
}

func TestGeminiResponseUsageIncludesCacheDetails(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"candidates":[
			{
				"content":{"role":"model","parts":[{"text":"hi"}]},
				"finishReason":"STOP"
			}
		],
		"usageMetadata":{
			"promptTokenCount":42,
			"cachedContentTokenCount":10,
			"candidatesTokenCount":17,
			"thoughtsTokenCount":4,
			"totalTokenCount":59
		}
	}`)

	anthResp := GeminiToAnthropicResponse(body, "claude-sonnet")
	require.Equal(t, int64(32), anthResp.Usage.InputTokens)
	require.Equal(t, int64(10), anthResp.Usage.CacheReadInputTokens)
	require.Equal(t, int64(21), anthResp.Usage.OutputTokens)

	ccResp := GeminiToOpenAIResponse(body, "gpt-4o")
	require.NotNil(t, ccResp.Usage)
	require.Equal(t, int64(42), ccResp.Usage.PromptTokens)
	require.Equal(t, int64(21), ccResp.Usage.CompletionTokens)
	require.Equal(t, int64(63), ccResp.Usage.TotalTokens)
	require.NotNil(t, ccResp.Usage.PromptTokensDetails)
	require.Equal(t, int64(10), ccResp.Usage.PromptTokensDetails.CachedTokens)
	require.NotNil(t, ccResp.Usage.CompletionTokensDetails)
	require.Equal(t, int64(4), ccResp.Usage.CompletionTokensDetails.ReasoningTokens)

	respResp := GeminiToResponsesResponse(body, "gpt-4o")
	require.NotNil(t, respResp.Usage)
	require.Equal(t, int64(42), respResp.Usage.InputTokens)
	require.Equal(t, int64(21), respResp.Usage.OutputTokens)
	require.Equal(t, int64(63), respResp.Usage.TotalTokens)
	require.NotNil(t, respResp.Usage.InputTokensDetails)
	require.Equal(t, int64(10), respResp.Usage.InputTokensDetails.CachedTokens)
	require.NotNil(t, respResp.Usage.OutputTokensDetails)
	require.Equal(t, int64(4), respResp.Usage.OutputTokensDetails.ReasoningTokens)
}
