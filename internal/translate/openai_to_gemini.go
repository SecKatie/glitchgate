// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// buildOAIToolNameMap scans all messages for assistant tool calls and builds
// a map from tool call ID to function name. This is required because OpenAI
// tool result messages carry only the ToolCallID, not the function name.
func buildOAIToolNameMap(messages []ChatMessage) map[string]string {
	m := make(map[string]string)
	for _, msg := range messages {
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" {
				m[tc.ID] = tc.Function.Name
			}
		}
	}
	return m
}

// OpenAIToGeminiRequest translates an OpenAI ChatCompletionRequest into a
// Gemini generateContent request.
//
// Role mapping:
//   - system → systemInstruction (joined with "\n")
//   - user   → contents[role=user]
//   - assistant → contents[role=model]
//   - tool   → contents[role=user] with functionResponse part
func OpenAIToGeminiRequest(req *ChatCompletionRequest) (*GeminiRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request must not be nil")
	}

	toolNameByID := buildOAIToolNameMap(req.Messages)

	var systemParts []string
	var contents []GeminiContent

	for _, msg := range req.Messages {
		switch msg.Role {
		case "system":
			text, err := extractTextContent(msg.Content)
			if err != nil {
				return nil, fmt.Errorf("invalid system message content: %w", err)
			}
			systemParts = append(systemParts, text)

		case "user":
			parts, err := oaiContentToGeminiParts(msg.Content)
			if err != nil {
				return nil, fmt.Errorf("invalid user message content: %w", err)
			}
			contents = append(contents, GeminiContent{
				Role:  "user",
				Parts: parts,
			})

		case "assistant":
			var parts []GeminiPart

			// Text content (may be empty when only tool calls are present).
			text, _ := extractTextContent(msg.Content)
			if text != "" {
				parts = append(parts, GeminiPart{Text: text})
			}

			// Function call parts for each tool call.
			for _, tc := range msg.ToolCalls {
				var args interface{}
				if tc.Function.Arguments != "" {
					if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
						// Fall back to the raw string wrapped in a map so the
						// Args field is always valid JSON for Gemini.
						args = map[string]interface{}{"_raw": tc.Function.Arguments}
					}
				}
				_, thoughtSignature := decodeGeminiToolCallID(tc.ID)
				part := GeminiPart{
					FunctionCall: &GeminiFunctionCall{
						Name: tc.Function.Name,
						Args: args,
					},
				}
				if thoughtSignature != "" {
					part.ThoughtSignature = thoughtSignature
				}
				parts = append(parts, part)
			}

			if len(parts) == 0 {
				// Gemini requires at least one part; use an empty text part.
				parts = []GeminiPart{{Text: ""}}
			}

			contents = append(contents, GeminiContent{
				Role:  "model",
				Parts: parts,
			})

		case "tool":
			text, err := extractTextContent(msg.Content)
			if err != nil {
				return nil, fmt.Errorf("invalid tool message content: %w", err)
			}
			fnName := toolNameByID[msg.ToolCallID]
			part := GeminiPart{
				FunctionResponse: &GeminiFuncResponse{
					Name:     fnName,
					Response: map[string]interface{}{"output": text},
				},
			}
			contents = append(contents, GeminiContent{
				Role:  "user",
				Parts: []GeminiPart{part},
			})

		default:
			return nil, fmt.Errorf("unsupported message role: %s", msg.Role)
		}
	}

	gemReq := &GeminiRequest{
		Contents: contents,
	}

	// System instruction.
	if len(systemParts) > 0 {
		gemReq.SystemInstruction = &GeminiContent{
			Parts: []GeminiPart{{Text: strings.Join(systemParts, "\n")}},
		}
	}

	// Generation config.
	var genCfg GeminiGenerationConfig
	hasGenCfg := false

	if req.MaxTokens != nil {
		genCfg.MaxOutputTokens = req.MaxTokens
		hasGenCfg = true
	}
	if req.Temperature != nil {
		genCfg.Temperature = req.Temperature
		hasGenCfg = true
	}
	if req.TopP != nil {
		genCfg.TopP = req.TopP
		hasGenCfg = true
	}
	if req.Stop != nil {
		seqs, err := parseStopSequences(req.Stop)
		if err != nil {
			return nil, fmt.Errorf("invalid stop field: %w", err)
		}
		if len(seqs) > 0 {
			genCfg.StopSequences = seqs
			hasGenCfg = true
		}
	}

	if hasGenCfg {
		gemReq.GenerationConfig = &genCfg
	}

	// Tools.
	if len(req.Tools) > 0 {
		var decls []GeminiFunctionDeclaration
		for _, t := range req.Tools {
			if t.Type != "function" {
				continue
			}
			decls = append(decls, GeminiFunctionDeclaration{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  sanitizeSchemaForGemini(t.Function.Parameters),
			})
		}
		if len(decls) > 0 {
			gemReq.Tools = []GeminiTool{{FunctionDeclarations: decls}}
		}
	}

	// reasoning_effort is intentionally skipped: Gemini exposes its own
	// thinking mechanism via thinkingConfig which is set by the caller if
	// needed. There is no direct mapping from OpenAI reasoning_effort.

	return gemReq, nil
}

// oaiContentToGeminiParts converts an OpenAI message content field (string or
// multipart array) into a slice of GeminiParts.
//
// For multipart content:
//   - text parts → GeminiPart{Text: ...}
//   - image_url parts with data URIs → GeminiPart{InlineData: ...}
//   - image_url parts with plain URLs are skipped (Gemini generateContent does
//     not support URL-referenced images inline)
func oaiContentToGeminiParts(content interface{}) ([]GeminiPart, error) {
	switch v := content.(type) {
	case string:
		return []GeminiPart{{Text: v}}, nil

	case []interface{}:
		var parts []GeminiPart
		for _, item := range v {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			t, _ := m["type"].(string)
			switch t {
			case "text":
				if text, ok := m["text"].(string); ok {
					parts = append(parts, GeminiPart{Text: text})
				}
			case "image_url":
				imgURL, ok := m["image_url"].(map[string]interface{})
				if !ok {
					continue
				}
				url, _ := imgURL["url"].(string)
				if url == "" {
					continue
				}
				if strings.HasPrefix(url, "data:") {
					blob, err := dataURIToGeminiBlob(url)
					if err != nil {
						return nil, err
					}
					parts = append(parts, GeminiPart{InlineData: blob})
				}
				// Plain URLs are skipped: Gemini generateContent does not
				// support fetching remote image URLs in the inline data path.
			}
		}
		if len(parts) == 0 {
			parts = []GeminiPart{{Text: ""}}
		}
		return parts, nil

	case nil:
		return []GeminiPart{{Text: ""}}, nil

	default:
		return nil, fmt.Errorf("unsupported content type for Gemini translation")
	}
}

// dataURIToGeminiBlob parses a data URI (e.g. "data:image/png;base64,<data>")
// and returns a GeminiBlob with mimeType and base64-encoded data.
func dataURIToGeminiBlob(uri string) (*GeminiBlob, error) {
	// data:<mediatype>;base64,<data>
	withoutScheme := strings.TrimPrefix(uri, "data:")
	idx := strings.Index(withoutScheme, ",")
	if idx < 0 {
		return nil, fmt.Errorf("invalid data URI: missing comma separator")
	}
	header := withoutScheme[:idx]
	data := withoutScheme[idx+1:]
	mediaType := strings.TrimSuffix(header, ";base64")
	return &GeminiBlob{
		MIMEType: mediaType,
		Data:     data,
	}, nil
}

// GeminiToOpenAIResponse translates a raw Gemini generateContent response body
// into an OpenAI ChatCompletionResponse.
func GeminiToOpenAIResponse(body []byte, model string) *ChatCompletionResponse {
	var gemResp GeminiResponse
	if err := json.Unmarshal(body, &gemResp); err != nil {
		// Return a minimal error-indicating response rather than crashing.
		stop := "stop"
		return &ChatCompletionResponse{
			ID:      "chatcmpl-gemini",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []Choice{{
				Index:        0,
				Message:      &ChatMessage{Role: "assistant", Content: ""},
				FinishReason: &stop,
			}},
		}
	}

	if len(gemResp.Candidates) == 0 {
		stop := "stop"
		return &ChatCompletionResponse{
			ID:      "chatcmpl-gemini",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []Choice{{
				Index:        0,
				Message:      &ChatMessage{Role: "assistant", Content: ""},
				FinishReason: &stop,
			}},
		}
	}

	candidate := gemResp.Candidates[0]

	var textContent string
	var toolCalls []ToolCall
	tcIdx := 0

	if candidate.Content != nil {
		for _, part := range candidate.Content.Parts {
			if part.FunctionCall != nil {
				argsBytes, _ := json.Marshal(part.FunctionCall.Args)
				id := fmt.Sprintf("call_%06d", tcIdx)
				tcIdx++
				id = encodeGeminiToolCallID(id, part.ThoughtSignature)
				toolCalls = append(toolCalls, ToolCall{
					ID:   id,
					Type: "function",
					Function: FunctionCall{
						Name:      part.FunctionCall.Name,
						Arguments: string(argsBytes),
					},
				})
			} else if part.Text != "" {
				textContent += part.Text
			}
		}
	}

	// Map Gemini finish reason to OpenAI finish reason.
	finishReason := mapGeminiFinishReason(candidate.FinishReason, len(toolCalls) > 0)

	// Build usage.
	var usage *OpenAIUsage
	if gemResp.UsageMetadata != nil {
		totalOutputTokens := gemResp.UsageMetadata.CandidatesTokenCount + gemResp.UsageMetadata.ThoughtsTokenCount
		totalTokens := gemResp.UsageMetadata.TotalTokenCount
		minTotalTokens := gemResp.UsageMetadata.PromptTokenCount + totalOutputTokens
		if totalTokens < minTotalTokens {
			totalTokens = minTotalTokens
		}
		usage = &OpenAIUsage{
			PromptTokens:     gemResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: totalOutputTokens,
			TotalTokens:      totalTokens,
		}
		if gemResp.UsageMetadata.CachedContentTokenCount > 0 {
			usage.PromptTokensDetails = &PromptTokensDetails{
				CachedTokens: gemResp.UsageMetadata.CachedContentTokenCount,
			}
		}
		if gemResp.UsageMetadata.ThoughtsTokenCount > 0 {
			usage.CompletionTokensDetails = &CompletionTokensDetails{
				ReasoningTokens: gemResp.UsageMetadata.ThoughtsTokenCount,
			}
		}
	}

	// Build the content field: use nil if there are only tool calls and no text.
	var msgContent interface{}
	if textContent != "" || len(toolCalls) == 0 {
		msgContent = textContent
	}

	return &ChatCompletionResponse{
		ID:      "chatcmpl-gemini",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []Choice{{
			Index: 0,
			Message: &ChatMessage{
				Role:      "assistant",
				Content:   msgContent,
				ToolCalls: toolCalls,
			},
			FinishReason: &finishReason,
		}},
		Usage: usage,
	}
}

// mapGeminiFinishReason converts a Gemini finishReason string to the
// corresponding OpenAI finish_reason value.
func mapGeminiFinishReason(geminiReason string, hasToolCalls bool) string {
	switch geminiReason {
	case "STOP":
		if hasToolCalls {
			return "tool_calls"
		}
		return "stop"
	case "MAX_TOKENS":
		return "length"
	default:
		return "stop"
	}
}
