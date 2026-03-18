// SPDX-License-Identifier: AGPL-3.0-or-later

// Package translate converts between Anthropic, OpenAI Chat Completions,
// OpenAI Responses, and Gemini API formats via a canonical intermediate
// representation.
//
// Public functions in this file are thin delegations that compose the
// canonical converters (canonical_*.go) into the pairwise translations
// expected by the proxy layer.
package translate

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
	"github.com/seckatie/glitchgate/internal/provider/gemini"
	"github.com/seckatie/glitchgate/internal/provider/openai"
)

// ---------------------------------------------------------------------------
// From Anthropic
// ---------------------------------------------------------------------------

// AnthropicToOpenAI translates an Anthropic MessagesResponse into an
// OpenAI openai.ChatCompletionResponse. The model parameter is the client-facing
// model name to include in the response.
func AnthropicToOpenAI(resp *anthropic.MessagesResponse, model string) *openai.ChatCompletionResponse {
	canon := AnthropicResponseToCanonical(resp)
	return CanonicalToOpenAIResponse(canon, model)
}

// AnthropicToOpenAIRequest translates an Anthropic MessagesRequest into an
// OpenAI openai.ChatCompletionRequest. This is the reverse of OpenAIToAnthropic and
// is used when an Anthropic-format client sends a request to an OpenAI-native
// provider (e.g. GitHub Copilot).
func AnthropicToOpenAIRequest(req *anthropic.MessagesRequest) (*openai.ChatCompletionRequest, error) {
	canon, err := AnthropicRequestToCanonical(req)
	if err != nil {
		return nil, err
	}
	return CanonicalToOpenAIRequest(canon)
}

// AnthropicToResponses translates an Anthropic MessagesRequest to a Responses API request.
func AnthropicToResponses(req *anthropic.MessagesRequest, upstreamModel string) (*openai.ResponsesRequest, error) {
	canon, err := AnthropicRequestToCanonical(req)
	if err != nil {
		return nil, err
	}
	return CanonicalToResponsesRequest(canon, upstreamModel)
}

// AnthropicToResponsesResponse translates an Anthropic Messages response body
// to a Responses API response.
func AnthropicToResponsesResponse(body []byte, model string) *openai.ResponsesResponse {
	var anthResp anthropic.MessagesResponse
	if err := json.Unmarshal(body, &anthResp); err != nil {
		return &openai.ResponsesResponse{
			Object: "response",
			Status: "failed",
			Error: &openai.ResponsesError{
				Code:    "server_error",
				Message: "Failed to parse upstream response",
			},
		}
	}

	canon := AnthropicResponseToCanonical(&anthResp)
	return CanonicalToResponsesResponse(canon, model)
}

// AnthropicToGeminiRequest converts an Anthropic MessagesRequest to a Gemini
// generateContent request body.
func AnthropicToGeminiRequest(req *anthropic.MessagesRequest) (*gemini.Request, error) {
	canon, err := AnthropicRequestToCanonical(req)
	if err != nil {
		return nil, err
	}
	return CanonicalToGeminiRequest(canon)
}

// AnthropicErrorToOpenAI translates an Anthropic error response body
// into an OpenAI-formatted error response.
func AnthropicErrorToOpenAI(body []byte) ([]byte, error) {
	var anthErr anthropic.ErrorResponse
	if err := json.Unmarshal(body, &anthErr); err != nil {
		// If we can't parse the Anthropic error, wrap the raw message.
		oaiErr := openai.ErrorResponse{
			Error: openai.Error{
				Message: string(body),
				Type:    "api_error",
			},
		}
		return json.Marshal(oaiErr)
	}

	oaiErr := openai.ErrorResponse{
		Error: openai.Error{
			Message: anthErr.Error.Message,
			Type:    mapAnthropicErrorType(anthErr.Error.Type),
			Code:    anthErr.Error.Type,
		},
	}
	return json.Marshal(oaiErr)
}

// ---------------------------------------------------------------------------
// From OpenAI
// ---------------------------------------------------------------------------

// OpenAIToAnthropic translates an OpenAI openai.ChatCompletionRequest into an
// Anthropic MessagesRequest.
func OpenAIToAnthropic(req *openai.ChatCompletionRequest) (*anthropic.MessagesRequest, error) {
	canon, err := OpenAIRequestToCanonical(req)
	if err != nil {
		return nil, err
	}
	return CanonicalToAnthropicRequest(canon)
}

// OpenAIToAnthropicResponse translates an OpenAI openai.ChatCompletionResponse into
// an Anthropic MessagesResponse.
func OpenAIToAnthropicResponse(resp *openai.ChatCompletionResponse, model string) *anthropic.MessagesResponse {
	canon := OpenAIResponseToCanonical(resp)
	return CanonicalToAnthropicResponse(canon, model)
}

// OpenAIToResponses translates a Chat Completions request to a Responses API request.
func OpenAIToResponses(req *openai.ChatCompletionRequest, upstreamModel string) (*openai.ResponsesRequest, error) {
	canon, err := OpenAIRequestToCanonical(req)
	if err != nil {
		return nil, err
	}
	return CanonicalToResponsesRequest(canon, upstreamModel)
}

// OpenAIToResponsesResponse translates an OpenAI Chat Completions response body
// to a Responses API response.
func OpenAIToResponsesResponse(body []byte, model string) *openai.ResponsesResponse {
	var ccResp openai.ChatCompletionResponse
	if err := json.Unmarshal(body, &ccResp); err != nil {
		return &openai.ResponsesResponse{
			Object: "response",
			Status: "failed",
			Error: &openai.ResponsesError{
				Code:    "server_error",
				Message: "Failed to parse upstream response",
			},
		}
	}

	canon := OpenAIResponseToCanonical(&ccResp)
	return CanonicalToResponsesResponse(canon, model)
}

// OpenAIToGeminiRequest translates an OpenAI openai.ChatCompletionRequest into a
// Gemini generateContent request.
func OpenAIToGeminiRequest(req *openai.ChatCompletionRequest) (*gemini.Request, error) {
	canon, err := OpenAIRequestToCanonical(req)
	if err != nil {
		return nil, err
	}
	return CanonicalToGeminiRequest(canon)
}

// ---------------------------------------------------------------------------
// From Responses
// ---------------------------------------------------------------------------

// ResponsesToAnthropic translates a Responses API request to an Anthropic MessagesRequest.
func ResponsesToAnthropic(req *openai.ResponsesRequest, upstreamModel string) (*anthropic.MessagesRequest, error) {
	canon, err := ResponsesRequestToCanonical(req)
	if err != nil {
		return nil, err
	}
	canon.Model = upstreamModel
	return CanonicalToAnthropicRequest(canon)
}

// ResponsesToAnthropicResponse translates a Responses API response body
// to an Anthropic MessagesResponse.
func ResponsesToAnthropicResponse(body []byte, model string) *anthropic.MessagesResponse {
	var resp openai.ResponsesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return &anthropic.MessagesResponse{
			Type: "error",
		}
	}

	canon := ResponsesResponseToCanonical(&resp)
	return CanonicalToAnthropicResponse(canon, model)
}

// ResponsesToOpenAI translates a Responses API request to an OpenAI Chat Completions request.
func ResponsesToOpenAI(req *openai.ResponsesRequest, upstreamModel string) (*openai.ChatCompletionRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request must not be nil")
	}

	// Chat Completions only supports function tools; reject unsupported types early.
	for _, t := range req.Tools {
		if t.Type != "function" {
			return nil, fmt.Errorf("tool type %q (%s) is not supported for Chat Completions upstream", t.Type, t.Name)
		}
	}

	canon, err := ResponsesRequestToCanonical(req)
	if err != nil {
		return nil, err
	}
	canon.Model = upstreamModel
	return CanonicalToOpenAIRequest(canon)
}

// ResponsesToOpenAIResponse translates a Responses API response body
// to a Chat Completions response.
func ResponsesToOpenAIResponse(body []byte, model string) *openai.ChatCompletionResponse {
	var resp openai.ResponsesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return &openai.ChatCompletionResponse{
			Object: "chat.completion",
		}
	}

	canon := ResponsesResponseToCanonical(&resp)
	return CanonicalToOpenAIResponse(canon, model)
}

// ResponsesToGeminiRequest translates a Responses API request to a Gemini generateContent request.
func ResponsesToGeminiRequest(req *openai.ResponsesRequest) (*gemini.Request, error) {
	canon, err := ResponsesRequestToCanonical(req)
	if err != nil {
		return nil, err
	}
	return CanonicalToGeminiRequest(canon)
}

// ---------------------------------------------------------------------------
// From Gemini
// ---------------------------------------------------------------------------

// GeminiToAnthropicResponse converts a Gemini generateContent response body to
// an Anthropic MessagesResponse.
func GeminiToAnthropicResponse(body []byte, model string) *anthropic.MessagesResponse {
	canon, err := GeminiResponseToCanonical(body)
	if err != nil {
		stopReason := "end_turn"
		return &anthropic.MessagesResponse{
			Type:       "message",
			Role:       "assistant",
			Model:      model,
			StopReason: &stopReason,
		}
	}
	return CanonicalToAnthropicResponse(canon, model)
}

// GeminiToOpenAIResponse translates a raw Gemini generateContent response body
// into an OpenAI openai.ChatCompletionResponse.
func GeminiToOpenAIResponse(body []byte, model string) *openai.ChatCompletionResponse {
	canon, err := GeminiResponseToCanonical(body)
	if err != nil {
		stop := "stop"
		return &openai.ChatCompletionResponse{
			ID:      "chatcmpl-gemini",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []openai.Choice{{
				Index:        0,
				Message:      &openai.ChatMessage{Role: "assistant", Content: ""},
				FinishReason: &stop,
			}},
		}
	}
	return CanonicalToOpenAIResponse(canon, model)
}

// GeminiToResponsesResponse converts a raw Gemini generateContent response body to a
// Responses API openai.ResponsesResponse.
func GeminiToResponsesResponse(body []byte, model string) *openai.ResponsesResponse {
	canon, err := GeminiResponseToCanonical(body)
	if err != nil {
		return &openai.ResponsesResponse{
			Object: "response",
			Status: "failed",
			Error: &openai.ResponsesError{
				Code:    "server_error",
				Message: "Failed to parse upstream Gemini response",
			},
		}
	}
	return CanonicalToResponsesResponse(canon, model)
}
