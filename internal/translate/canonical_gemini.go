// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"fmt"

	"github.com/seckatie/glitchgate/internal/provider/gemini"
)

func geminiFinishReasonToCanonical(reason string, hasFunctionCall bool) StopReason {
	switch reason {
	case "MAX_TOKENS":
		return StopReasonMaxTokens
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
		return StopReasonContentFilter
	case "STOP":
		if hasFunctionCall {
			return StopReasonToolUse
		}
		return StopReasonEndTurn
	default:
		if hasFunctionCall {
			return StopReasonToolUse
		}
		return StopReasonEndTurn
	}
}

// CanonicalToGeminiRequest converts a canonical request to a Gemini generateContent request.
func CanonicalToGeminiRequest(canon *CanonicalRequest) (*gemini.Request, error) {
	if canon == nil {
		return nil, fmt.Errorf("canonical request must not be nil")
	}

	gReq := &gemini.Request{}

	// System instruction.
	if canon.System != "" {
		gReq.SystemInstruction = &gemini.Content{
			Parts: []gemini.Part{{Text: canon.System}},
		}
	}

	// Build tool-name-by-ID map from assistant messages (needed for tool_result resolution).
	toolNameByID := make(map[string]string)
	for _, msg := range canon.Messages {
		if msg.Role == "assistant" {
			for _, b := range msg.Blocks {
				if b.Type == BlockToolUse && b.ToolID != "" && b.ToolName != "" {
					toolNameByID[b.ToolID] = b.ToolName
				}
			}
		}
	}

	// Convert messages to contents.
	for _, msg := range canon.Messages {
		role := "user"
		if msg.Role == "assistant" {
			role = "model"
		}

		parts, err := canonicalBlocksToGeminiParts(msg.Blocks, toolNameByID)
		if err != nil {
			return nil, fmt.Errorf("message role=%q: %w", msg.Role, err)
		}

		if len(parts) == 0 {
			parts = []gemini.Part{{Text: ""}}
		}

		gReq.Contents = append(gReq.Contents, gemini.Content{
			Role:  role,
			Parts: parts,
		})
	}

	// Generation config.
	var genCfg gemini.GenerationConfig
	hasGenCfg := false

	if canon.MaxTokens != nil {
		genCfg.MaxOutputTokens = canon.MaxTokens
		hasGenCfg = true
	}
	if canon.Temperature != nil {
		genCfg.Temperature = canon.Temperature
		hasGenCfg = true
	}
	if canon.TopP != nil {
		genCfg.TopP = canon.TopP
		hasGenCfg = true
	}
	if canon.TopK != nil {
		genCfg.TopK = canon.TopK
		hasGenCfg = true
	}
	if len(canon.StopSequences) > 0 {
		genCfg.StopSequences = canon.StopSequences
		hasGenCfg = true
	}
	if canon.Thinking != nil && canon.Thinking.Enabled {
		budget := canon.Thinking.BudgetTokens
		if budget > 0 {
			genCfg.ThinkingConfig = &gemini.ThinkingConfig{ThinkingBudget: &budget}
			hasGenCfg = true
		}
	}

	if hasGenCfg {
		gReq.GenerationConfig = &genCfg
	}

	// Tools.
	if len(canon.Tools) > 0 {
		var decls []gemini.FunctionDeclaration
		for _, t := range canon.Tools {
			decls = append(decls, gemini.FunctionDeclaration{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  gemini.SanitizeSchemaForGemini(t.Parameters),
			})
		}
		gReq.Tools = []gemini.Tool{{FunctionDeclarations: decls}}
	}

	return gReq, nil
}

// canonicalBlocksToGeminiParts converts canonical blocks to Gemini parts.
func canonicalBlocksToGeminiParts(blocks []CanonicalBlock, toolNameByID map[string]string) ([]gemini.Part, error) {
	var parts []gemini.Part

	for _, b := range blocks {
		switch b.Type {
		case BlockText:
			if b.Text != "" {
				parts = append(parts, gemini.Part{Text: b.Text})
			}
		case BlockImage:
			if b.ImageData != "" {
				parts = append(parts, gemini.Part{
					InlineData: &gemini.Blob{
						MIMEType: b.ImageMediaType,
						Data:     b.ImageData,
					},
				})
			}
			// Plain URL images are not supported by Gemini generateContent.
		case BlockDocument:
			if b.DocData != "" {
				parts = append(parts, gemini.Part{
					InlineData: &gemini.Blob{
						MIMEType: b.DocMediaType,
						Data:     b.DocData,
					},
				})
			}
			// Plain URL documents are not supported by Gemini generateContent.
		case BlockToolUse:
			_, thoughtSignature := gemini.DecodeToolCallID(b.ToolID)
			part := gemini.Part{
				FunctionCall: &gemini.FunctionCall{
					Name: b.ToolName,
					Args: b.ToolInput,
				},
			}
			if thoughtSignature != "" {
				part.ThoughtSignature = thoughtSignature
			}
			parts = append(parts, part)
		case BlockToolResult:
			fnName := toolNameByID[b.ToolUseID]
			var responseValue any
			if b.ToolResultText != "" {
				if err := json.Unmarshal([]byte(b.ToolResultText), &responseValue); err != nil {
					responseValue = map[string]any{"output": b.ToolResultText}
				}
			} else {
				responseValue = map[string]any{}
			}
			parts = append(parts, gemini.Part{
				FunctionResponse: &gemini.FuncResponse{
					Name:     fnName,
					Response: responseValue,
				},
			})
		case BlockThinking:
			// Gemini handles thinking natively; skip.
		}
	}

	return parts, nil
}

// GeminiResponseToCanonical converts a raw Gemini generateContent response body
// to canonical form.
func GeminiResponseToCanonical(body []byte) (*CanonicalResponse, error) {
	var gr gemini.Response
	if err := json.Unmarshal(body, &gr); err != nil {
		return nil, fmt.Errorf("parsing Gemini response: %w", err)
	}

	canon := &CanonicalResponse{}

	if gr.ModelVersion != "" {
		canon.Model = gr.ModelVersion
	}

	// Usage.
	if gr.UsageMetadata != nil {
		input, output, cacheRead, reasoning := gemini.UsageTotals(gr.UsageMetadata)
		canon.Usage = CanonicalUsage{
			InputTokens:          input,
			OutputTokens:         output,
			CacheReadInputTokens: cacheRead,
			ReasoningTokens:      reasoning,
		}
	}

	if len(gr.Candidates) == 0 {
		canon.StopReason = StopReasonEndTurn
		return canon, nil
	}

	cand := gr.Candidates[0]
	hasFunctionCall := false

	if cand.Content != nil {
		for idx, part := range cand.Content.Parts {
			switch {
			case part.FunctionCall != nil:
				hasFunctionCall = true
				var inputVal any
				if part.FunctionCall.Args != nil {
					raw, err := json.Marshal(part.FunctionCall.Args)
					if err == nil {
						_ = json.Unmarshal(raw, &inputVal)
					}
				}
				if inputVal == nil {
					inputVal = map[string]any{}
				}
				canon.Content = append(canon.Content, CanonicalBlock{
					Type:      BlockToolUse,
					ToolID:    gemini.EncodeToolCallID(fmt.Sprintf("toolu_%06d", idx), part.ThoughtSignature),
					ToolName:  part.FunctionCall.Name,
					ToolInput: inputVal,
				})
			case part.Thought != nil && *part.Thought:
				canon.Content = append(canon.Content, CanonicalBlock{
					Type:         BlockThinking,
					ThinkingText: part.Text,
				})
			case part.Text != "":
				canon.Content = append(canon.Content, CanonicalBlock{Type: BlockText, Text: part.Text})
			}
		}
	}

	// Finish reason.
	canon.StopReason = geminiFinishReasonToCanonical(cand.FinishReason, hasFunctionCall)

	return canon, nil
}
