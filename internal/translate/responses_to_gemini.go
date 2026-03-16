// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// ResponsesToGeminiRequest translates a Responses API request to a Gemini generateContent request.
func ResponsesToGeminiRequest(req *ResponsesRequest) (*GeminiRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request must not be nil")
	}

	result := &GeminiRequest{}

	// Map instructions to systemInstruction.
	if req.Instructions != nil && *req.Instructions != "" {
		result.SystemInstruction = &GeminiContent{
			Parts: []GeminiPart{{Text: *req.Instructions}},
		}
	}

	// Parse input and convert to Gemini contents.
	contents, err := responsesInputToGeminiContents(req.Input)
	if err != nil {
		return nil, fmt.Errorf("translating input: %w", err)
	}
	result.Contents = contents

	// Map generation config fields.
	genConfig := &GeminiGenerationConfig{
		MaxOutputTokens: req.MaxOutputTokens,
		Temperature:     req.Temperature,
		TopP:            req.TopP,
	}
	// Only attach if at least one field is set.
	if genConfig.MaxOutputTokens != nil || genConfig.Temperature != nil || genConfig.TopP != nil {
		result.GenerationConfig = genConfig
	}

	// Translate function tools into a single GeminiTool with functionDeclarations.
	var funcDecls []GeminiFunctionDeclaration
	for _, t := range req.Tools {
		if t.Type != "function" {
			// Built-in tools (web_search, computer_use) cannot be translated to Gemini function
			// declarations; skip with a warning.
			slog.Warn("skipping unsupported tool type for Gemini upstream", "tool_type", t.Type, "tool_name", t.Name)
			continue
		}
		var params interface{}
		if len(t.Parameters) > 0 {
			if err := json.Unmarshal(t.Parameters, &params); err != nil {
				params = map[string]interface{}{}
			}
		}
		funcDecls = append(funcDecls, GeminiFunctionDeclaration{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  sanitizeSchemaForGemini(params),
		})
	}
	if len(funcDecls) > 0 {
		result.Tools = []GeminiTool{{FunctionDeclarations: funcDecls}}
	}

	// Reasoning effort is skipped — Gemini manages its own thinking budget separately.

	return result, nil
}

// responsesInputToGeminiContents parses the Responses API input field and converts it to
// a slice of GeminiContent values.
func responsesInputToGeminiContents(input json.RawMessage) ([]GeminiContent, error) {
	if len(input) == 0 {
		return nil, nil
	}

	// Try parsing as a plain string first.
	var textInput string
	if err := json.Unmarshal(input, &textInput); err == nil {
		return []GeminiContent{{
			Role:  "user",
			Parts: []GeminiPart{{Text: textInput}},
		}}, nil
	}

	// Parse as array of InputItems.
	var items []InputItem
	if err := json.Unmarshal(input, &items); err != nil {
		return nil, fmt.Errorf("input must be a string or array of input items: %w", err)
	}

	var contents []GeminiContent
	callNameByID := make(map[string]string, len(items))
	for _, item := range items {
		if item.Type == "function_call" && item.CallID != "" && item.Name != "" {
			callNameByID[item.CallID] = item.Name
		}
	}

	for _, item := range items {
		switch item.Type {
		case "message":
			msgContents, err := responsesMessageToGeminiContents(item)
			if err != nil {
				return nil, err
			}
			contents = append(contents, msgContents...)

		case "input_text":
			contents = append(contents, GeminiContent{
				Role:  "user",
				Parts: []GeminiPart{{Text: item.Text}},
			})

		case "input_image":
			if item.ImageURL == "" {
				return nil, fmt.Errorf("input_image requires image_url")
			}
			// Gemini inlineData requires base64; URL-only images are not natively supported
			// in the generateContent REST API without fetching. Surface as an error so callers
			// know this content cannot be forwarded.
			return nil, fmt.Errorf("input_image with image_url is not supported for Gemini upstream; use inlineData")

		case "input_file":
			return nil, fmt.Errorf("input_file content type is not supported by Gemini upstream")

		case "input_audio":
			return nil, fmt.Errorf("input_audio content type is not supported by Gemini upstream via Responses API translation")

		case "function_call":
			// Map to model turn with a functionCall part.
			var args interface{}
			if item.Arguments != "" {
				if err := json.Unmarshal([]byte(item.Arguments), &args); err != nil {
					args = map[string]interface{}{}
				}
			}
			_, thoughtSignature := decodeGeminiToolCallID(item.CallID)
			part := GeminiPart{
				FunctionCall: &GeminiFunctionCall{
					Name: item.Name,
					Args: args,
				},
			}
			if thoughtSignature != "" {
				part.ThoughtSignature = thoughtSignature
			}
			contents = append(contents, GeminiContent{
				Role:  "model",
				Parts: []GeminiPart{part},
			})

		case "function_call_output":
			// Map to user turn with a functionResponse part.
			// The output field is a string; wrap it in a map so Gemini receives a structured
			// response object as required by the API.
			var responseValue interface{}
			if item.Output != "" {
				if err := json.Unmarshal([]byte(item.Output), &responseValue); err != nil {
					// Not valid JSON — wrap as a plain string value.
					responseValue = map[string]interface{}{"output": item.Output}
				}
			} else {
				responseValue = map[string]interface{}{}
			}
			fnName := item.Name
			if fnName == "" {
				fnName = callNameByID[item.CallID]
			}
			contents = append(contents, GeminiContent{
				Role: "user",
				Parts: []GeminiPart{{
					FunctionResponse: &GeminiFuncResponse{
						Name:     fnName,
						Response: responseValue,
					},
				}},
			})

		case "item_reference":
			// Responses-only feature; silently drop.

		default:
			return nil, fmt.Errorf("unsupported input item type: %s", item.Type)
		}
	}

	return contents, nil
}

// responsesMessageToGeminiContents converts a Responses API message input item to one or more
// GeminiContent values. System-role messages are promoted to systemInstruction by the caller;
// here they are returned as user-role contents because Gemini does not accept a "system" role
// in the contents array.
func responsesMessageToGeminiContents(item InputItem) ([]GeminiContent, error) {
	role := item.Role
	if role == "" {
		role = "user"
	}

	// Map Responses roles to Gemini roles.
	geminiRole := responsesRoleToGemini(role)

	if len(item.Content) == 0 {
		return []GeminiContent{{Role: geminiRole, Parts: []GeminiPart{{Text: ""}}}}, nil
	}

	// Try parsing content as array of InputItems.
	var contentItems []InputItem
	if err := json.Unmarshal(item.Content, &contentItems); err != nil {
		// Try as plain string.
		var text string
		if err2 := json.Unmarshal(item.Content, &text); err2 == nil {
			return []GeminiContent{{
				Role:  geminiRole,
				Parts: []GeminiPart{{Text: text}},
			}}, nil
		}
		return nil, fmt.Errorf("invalid message content: %w", err)
	}

	var parts []GeminiPart
	for _, ci := range contentItems {
		switch ci.Type {
		case "input_text":
			parts = append(parts, GeminiPart{Text: ci.Text})
		case "input_image":
			return nil, fmt.Errorf("input_image with image_url is not supported for Gemini upstream; use inlineData")
		case "input_file":
			return nil, fmt.Errorf("input_file content type is not supported by Gemini upstream")
		default:
			// Fall back to treating the item as text.
			parts = append(parts, GeminiPart{Text: ci.Text})
		}
	}

	if len(parts) == 0 {
		parts = []GeminiPart{{Text: ""}}
	}

	return []GeminiContent{{Role: geminiRole, Parts: parts}}, nil
}

// responsesRoleToGemini maps a Responses API role string to the Gemini role convention.
// Gemini uses "user" and "model" (not "assistant").
func responsesRoleToGemini(role string) string {
	switch role {
	case "assistant":
		return "model"
	case "system":
		// Gemini does not support a "system" role in contents; treat as user.
		return "user"
	default:
		return "user"
	}
}

// GeminiToResponsesResponse converts a raw Gemini generateContent response body to a
// Responses API ResponsesResponse.
func GeminiToResponsesResponse(body []byte, model string) *ResponsesResponse {
	var gemResp GeminiResponse
	if err := json.Unmarshal(body, &gemResp); err != nil {
		return &ResponsesResponse{
			Object: "response",
			Status: "failed",
			Error: &ResponsesError{
				Code:    "server_error",
				Message: "Failed to parse upstream Gemini response",
			},
		}
	}

	resp := &ResponsesResponse{
		Object:    "response",
		CreatedAt: float64(time.Now().Unix()),
		Model:     model,
	}

	if len(gemResp.Candidates) == 0 {
		resp.Status = "failed"
		resp.Error = &ResponsesError{
			Code:    "server_error",
			Message: "Gemini response contained no candidates",
		}
		return resp
	}

	candidate := gemResp.Candidates[0]
	resp.Status = geminiFinishReasonToResponsesStatus(candidate.FinishReason)

	var output []OutputItem

	if candidate.Content != nil {
		var textContents []OutputContent

		for _, part := range candidate.Content.Parts {
			switch {
			case part.FunctionCall != nil:
				args, err := json.Marshal(part.FunctionCall.Args)
				if err != nil {
					args = []byte("{}")
				}
				callID := encodeGeminiToolCallID("fc_"+part.FunctionCall.Name, part.ThoughtSignature)
				output = append(output, OutputItem{
					Type:      "function_call",
					ID:        callID,
					CallID:    callID,
					Name:      part.FunctionCall.Name,
					Arguments: string(args),
					Status:    "completed",
				})

			case part.Thought != nil && *part.Thought:
				// Thinking/reasoning part — map to a reasoning output item.
				output = append(output, OutputItem{
					Type: "reasoning",
					Summary: []ReasoningSummary{{
						Type: "summary_text",
						Text: part.Text,
					}},
				})

			case part.Text != "":
				textContents = append(textContents, OutputContent{
					Type:        "output_text",
					Text:        part.Text,
					Annotations: []any{},
				})
			}
		}

		// Collect text parts into a single message output item.
		if len(textContents) > 0 {
			output = append(output, OutputItem{
				Type:    "message",
				ID:      "msg_gemini",
				Role:    "assistant",
				Content: textContents,
				Status:  "completed",
			})
		}
	}

	resp.Output = output

	// Map usage metadata.
	if gemResp.UsageMetadata != nil {
		u := gemResp.UsageMetadata
		totalOutputTokens := u.CandidatesTokenCount + u.ThoughtsTokenCount
		totalTokens := u.TotalTokenCount
		minTotalTokens := u.PromptTokenCount + totalOutputTokens
		if totalTokens < minTotalTokens {
			totalTokens = minTotalTokens
		}
		resp.Usage = &ResponsesUsage{
			InputTokens:  u.PromptTokenCount,
			OutputTokens: totalOutputTokens,
			TotalTokens:  totalTokens,
		}
		if u.CachedContentTokenCount > 0 {
			resp.Usage.InputTokensDetails = &InputTokensDetails{
				CachedTokens: u.CachedContentTokenCount,
			}
		}
		if u.ThoughtsTokenCount > 0 {
			resp.Usage.OutputTokensDetails = &OutputTokensDetails{
				ReasoningTokens: u.ThoughtsTokenCount,
			}
		}
	}

	return resp
}

// geminiFinishReasonToResponsesStatus maps a Gemini finishReason string to a Responses API
// status value.
func geminiFinishReasonToResponsesStatus(finishReason string) string {
	switch finishReason {
	case "STOP":
		return "completed"
	case "MAX_TOKENS":
		return "incomplete"
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
		return "incomplete"
	case "TOOL_CODE_EXECUTION_FAILED", "OTHER":
		return "failed"
	default:
		// FINISH_REASON_UNSPECIFIED or unknown values: treat as completed.
		return "completed"
	}
}
