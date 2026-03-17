// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"fmt"
	"strings"

	anthropic "github.com/seckatie/glitchgate/internal/provider/anthropic"
)

// AnthropicToGeminiRequest converts an Anthropic MessagesRequest to a Gemini
// generateContent request body. The caller is responsible for setting the model
// in the request URL; no model field is included in the returned GeminiRequest.
func AnthropicToGeminiRequest(req *anthropic.MessagesRequest) (*GeminiRequest, error) {
	gemini := &GeminiRequest{}

	// --- system instruction ---
	if req.System != nil {
		text := extractSystemText(req.System)
		if text != "" {
			gemini.SystemInstruction = &GeminiContent{
				Parts: []GeminiPart{{Text: text}},
			}
		}
	}

	// --- build tool-name-by-id map from assistant messages ---
	toolNameByID := buildToolNameByID(req.Messages)

	// --- messages → contents ---
	for _, msg := range req.Messages {
		role := "user"
		if msg.Role == "assistant" {
			role = "model"
		}

		parts, err := convertContentToParts(msg.Content, toolNameByID)
		if err != nil {
			return nil, fmt.Errorf("message role=%q: %w", msg.Role, err)
		}

		if len(parts) > 0 {
			gemini.Contents = append(gemini.Contents, GeminiContent{
				Role:  role,
				Parts: parts,
			})
		}
	}

	// --- generation config ---
	gc := &GeminiGenerationConfig{}
	hasGC := false

	if req.MaxTokens > 0 {
		maxOut := req.MaxTokens
		gc.MaxOutputTokens = &maxOut
		hasGC = true
	}
	if req.Temperature != nil {
		gc.Temperature = req.Temperature
		hasGC = true
	}
	if req.TopP != nil {
		gc.TopP = req.TopP
		hasGC = true
	}
	if req.TopK != nil {
		gc.TopK = req.TopK
		hasGC = true
	}
	if len(req.StopSequences) > 0 {
		gc.StopSequences = req.StopSequences
		hasGC = true
	}
	if req.Thinking != nil && req.Thinking.BudgetTokens > 0 {
		budget := req.Thinking.BudgetTokens
		gc.ThinkingConfig = &GeminiThinkingConfig{ThinkingBudget: &budget}
		hasGC = true
	}

	if hasGC {
		gemini.GenerationConfig = gc
	}

	// --- tools ---
	if len(req.Tools) > 0 {
		decls := make([]GeminiFunctionDeclaration, 0, len(req.Tools))
		for _, t := range req.Tools {
			decls = append(decls, GeminiFunctionDeclaration{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  sanitizeSchemaForGemini(t.InputSchema),
			})
		}
		gemini.Tools = []GeminiTool{{FunctionDeclarations: decls}}
	}

	return gemini, nil
}

// GeminiToAnthropicResponse converts a Gemini generateContent response body to
// an Anthropic MessagesResponse.
func GeminiToAnthropicResponse(body []byte, model string) *anthropic.MessagesResponse {
	var gr GeminiResponse
	if err := json.Unmarshal(body, &gr); err != nil {
		stopReason := "end_turn"
		return &anthropic.MessagesResponse{
			Type:       "message",
			Role:       "assistant",
			Model:      model,
			StopReason: &stopReason,
		}
	}

	resp := &anthropic.MessagesResponse{
		Type:  "message",
		Role:  "assistant",
		Model: model,
	}

	if gr.ModelVersion != "" {
		resp.Model = gr.ModelVersion
	}

	// --- usage ---
	if gr.UsageMetadata != nil {
		inputTokens, outputTokens, cacheReadTokens, _ := GeminiUsageTotals(gr.UsageMetadata)
		resp.Usage = anthropic.Usage{
			InputTokens:          inputTokens,
			OutputTokens:         outputTokens,
			CacheReadInputTokens: cacheReadTokens,
		}
	}

	if len(gr.Candidates) == 0 {
		stopReason := "end_turn"
		resp.StopReason = &stopReason
		return resp
	}

	cand := gr.Candidates[0]

	// --- content parts → content blocks ---
	hasFunctionCall := false
	if cand.Content != nil {
		for idx, part := range cand.Content.Parts {
			switch {
			case part.FunctionCall != nil:
				hasFunctionCall = true
				// Re-marshal Args into a map for Input field.
				var inputVal interface{}
				if part.FunctionCall.Args != nil {
					raw, err := json.Marshal(part.FunctionCall.Args)
					if err == nil {
						_ = json.Unmarshal(raw, &inputVal)
					}
				}
				if inputVal == nil {
					inputVal = map[string]interface{}{}
				}
				resp.Content = append(resp.Content, anthropic.ContentBlock{
					Type:  "tool_use",
					ID:    encodeGeminiToolCallID(fmt.Sprintf("toolu_%06d", idx), part.ThoughtSignature),
					Name:  part.FunctionCall.Name,
					Input: inputVal,
				})
			case part.Text != "":
				resp.Content = append(resp.Content, anthropic.ContentBlock{
					Type: "text",
					Text: part.Text,
				})
			}
		}
	}

	// --- finish reason ---
	stopReason := mapFinishReason(cand.FinishReason, hasFunctionCall)
	resp.StopReason = &stopReason

	return resp
}

// --- helpers ---

// extractSystemText pulls plain text out of an Anthropic system field, which
// may be a string or a []interface{} of system blocks after JSON decode.
func extractSystemText(system interface{}) string {
	switch v := system.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, item := range v {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if t, _ := m["type"].(string); t == "text" {
				if text, _ := m["text"].(string); text != "" {
					if strings.HasPrefix(text, "x-anthropic-") {
						continue
					}
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

// buildToolNameByID scans all messages to build a map from tool_use id → function name,
// needed to resolve tool_result blocks which only carry the id.
func buildToolNameByID(messages []anthropic.Message) map[string]string {
	m := make(map[string]string)
	for _, msg := range messages {
		if msg.Role != "assistant" {
			continue
		}
		switch v := msg.Content.(type) {
		case []interface{}:
			for _, item := range v {
				block, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				if blockType, _ := block["type"].(string); blockType == "tool_use" {
					id, _ := block["id"].(string)
					name, _ := block["name"].(string)
					if id != "" && name != "" {
						m[id] = name
					}
				}
			}
		}
	}
	return m
}

// convertContentToParts converts a Message.Content value (string or []interface{})
// to a slice of GeminiPart.
func convertContentToParts(content interface{}, toolNameByID map[string]string) ([]GeminiPart, error) {
	switch v := content.(type) {
	case string:
		if v == "" {
			return nil, nil
		}
		return []GeminiPart{{Text: v}}, nil

	case []interface{}:
		var parts []GeminiPart
		for _, item := range v {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			blockType, _ := m["type"].(string)
			switch blockType {
			case "text":
				text, _ := m["text"].(string)
				if text != "" {
					parts = append(parts, GeminiPart{Text: text})
				}

			case "thinking":
				// Skip: Gemini handles thinking natively.

			case "tool_use":
				name, _ := m["name"].(string)
				args := m["input"]
				toolID, _ := m["id"].(string)
				_, thoughtSignature := decodeGeminiToolCallID(toolID)
				part := GeminiPart{
					FunctionCall: &GeminiFunctionCall{
						Name: name,
						Args: args,
					},
				}
				if thoughtSignature != "" {
					part.ThoughtSignature = thoughtSignature
				}
				parts = append(parts, part)

			case "tool_result":
				toolUseID, _ := m["tool_use_id"].(string)
				fnName := toolNameByID[toolUseID]
				textContent := extractRawToolResultText(m["content"])
				parts = append(parts, GeminiPart{
					FunctionResponse: &GeminiFuncResponse{
						Name:     fnName,
						Response: map[string]interface{}{"output": textContent},
					},
				})

			case "image":
				src, ok := m["source"].(map[string]interface{})
				if !ok {
					continue
				}
				srcType, _ := src["type"].(string)
				if srcType == "base64" {
					mimeType, _ := src["media_type"].(string)
					data, _ := src["data"].(string)
					parts = append(parts, GeminiPart{
						InlineData: &GeminiBlob{
							MIMEType: mimeType,
							Data:     data,
						},
					})
				}
			}
		}
		return parts, nil

	default:
		return nil, nil
	}
}

// extractRawToolResultText pulls the text string out of a tool_result content field,
// which may be a plain string or a []interface{} of content blocks.
func extractRawToolResultText(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, item := range v {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if t, _ := m["type"].(string); t == "text" {
				if text, _ := m["text"].(string); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

// mapFinishReason converts a Gemini finishReason string to an Anthropic stop_reason.
func mapFinishReason(reason string, hasFunctionCall bool) string {
	switch reason {
	case "MAX_TOKENS":
		return "max_tokens"
	case "STOP":
		if hasFunctionCall {
			return "tool_use"
		}
		return "end_turn"
	default:
		if hasFunctionCall {
			return "tool_use"
		}
		return "end_turn"
	}
}
