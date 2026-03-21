// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/seckatie/glitchgate/internal/provider/openai"
)

// canonicalStopReasonToResponsesStatus maps canonical stop reason to Responses API status.
func canonicalStopReasonToResponsesStatus(sr StopReason) string {
	switch sr {
	case StopReasonMaxTokens:
		return "incomplete"
	case StopReasonContentFilter:
		return "incomplete"
	default:
		return "completed"
	}
}

// responsesStatusToCanonical maps Responses API status to canonical stop reason.
func responsesStatusToCanonical(status string, output []openai.OutputItem) StopReason {
	// Check for tool use.
	for _, item := range output {
		if item.Type == "function_call" {
			return StopReasonToolUse
		}
	}
	switch status {
	case "incomplete":
		return StopReasonMaxTokens
	default:
		return StopReasonEndTurn
	}
}

// ResponsesRequestToCanonical converts a Responses API request to canonical form.
func ResponsesRequestToCanonical(req *openai.ResponsesRequest) (*CanonicalRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request must not be nil")
	}

	canon := &CanonicalRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream != nil && *req.Stream,
	}

	if req.MaxOutputTokens != nil {
		canon.MaxTokens = req.MaxOutputTokens
	}

	if req.Instructions != nil && *req.Instructions != "" {
		canon.System = *req.Instructions
	}

	// Parse input.
	if len(req.Input) > 0 {
		msgs, err := responsesInputToCanonicalMessages(req.Input)
		if err != nil {
			return nil, fmt.Errorf("translating input: %w", err)
		}
		canon.Messages = msgs
	}

	// Convert tools.
	for _, t := range req.Tools {
		if t.Type != "function" {
			slog.Warn("skipping unsupported tool type for canonical conversion", "tool_type", t.Type, "tool_name", t.Name)
			continue
		}
		canon.Tools = append(canon.Tools, CanonicalTool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  unmarshalSchema(t.Parameters),
		})
	}

	// Convert tool_choice.
	if req.ToolChoice != nil {
		tc, err := responsesToolChoiceToCanonical(req.ToolChoice)
		if err != nil {
			return nil, err
		}
		canon.ToolChoice = tc
	}

	// Convert reasoning.
	if req.Reasoning != nil && req.Reasoning.Effort != nil && *req.Reasoning.Effort != "" {
		canon.Thinking = &CanonicalThinking{
			Enabled: true,
			Effort:  *req.Reasoning.Effort,
		}
	}

	return canon, nil
}

func responsesInputToCanonicalMessages(input json.RawMessage) ([]CanonicalMessage, error) {
	// Try as string.
	var textInput string
	if err := json.Unmarshal(input, &textInput); err == nil {
		return []CanonicalMessage{{
			Role:   "user",
			Blocks: []CanonicalBlock{{Type: BlockText, Text: textInput}},
		}}, nil
	}

	// Parse as array of InputItems.
	var items []openai.InputItem
	if err := json.Unmarshal(input, &items); err != nil {
		return nil, fmt.Errorf("input must be a string or array of input items: %w", err)
	}

	var messages []CanonicalMessage
	for _, item := range items {
		switch item.Type {
		case "message":
			msgs, err := responsesMessageItemToCanonical(item)
			if err != nil {
				return nil, err
			}
			messages = append(messages, msgs...)

		case "input_text":
			messages = append(messages, CanonicalMessage{
				Role:   "user",
				Blocks: []CanonicalBlock{{Type: BlockText, Text: item.Text}},
			})

		case "input_image":
			if item.ImageURL == "" {
				return nil, fmt.Errorf("input_image requires image_url")
			}
			cb := CanonicalBlock{Type: BlockImage}
			if parseImageURL(item.ImageURL, &cb) {
				messages = append(messages, CanonicalMessage{
					Role:   "user",
					Blocks: []CanonicalBlock{cb},
				})
			}

		case "input_file":
			cb := CanonicalBlock{Type: BlockDocument}
			cb.DocFilename = item.Filename
			if item.FileData != "" {
				if strings.HasPrefix(item.FileData, "data:") {
					parts := strings.SplitN(item.FileData, ",", 2)
					if len(parts) == 2 {
						mediaInfo := strings.TrimPrefix(parts[0], "data:")
						mediaInfo = strings.TrimSuffix(mediaInfo, ";base64")
						cb.DocMediaType = mediaInfo
						cb.DocData = parts[1]
					}
				}
			} else if item.FileURL != "" {
				cb.DocURL = item.FileURL
			}
			messages = append(messages, CanonicalMessage{
				Role:   "user",
				Blocks: []CanonicalBlock{cb},
			})

		case "input_audio":
			messages = append(messages, CanonicalMessage{
				Role: "user",
				Blocks: []CanonicalBlock{{
					Type:        BlockAudio,
					AudioData:   item.Data,
					AudioFormat: item.Format,
				}},
			})

		case "function_call":
			messages = append(messages, CanonicalMessage{
				Role: "assistant",
				Blocks: []CanonicalBlock{{
					Type:      BlockToolUse,
					ToolID:    item.CallID,
					ToolName:  item.Name,
					ToolInput: unmarshalToolArgs(item.Arguments),
				}},
			})

		case "function_call_output":
			messages = append(messages, CanonicalMessage{
				Role: "user",
				Blocks: []CanonicalBlock{{
					Type:           BlockToolResult,
					ToolUseID:      item.CallID,
					ToolResultText: item.Output,
				}},
			})

		case "item_reference":
			// Silently drop.

		default:
			return nil, fmt.Errorf("unsupported input item type: %s", item.Type)
		}
	}

	return messages, nil
}

func responsesMessageItemToCanonical(item openai.InputItem) ([]CanonicalMessage, error) {
	role := item.Role
	if role == "" {
		role = "user"
	}

	if len(item.Content) == 0 {
		return []CanonicalMessage{{Role: role, Blocks: []CanonicalBlock{{Type: BlockText, Text: ""}}}}, nil
	}

	// Try as string.
	var text string
	if err := json.Unmarshal(item.Content, &text); err == nil {
		return []CanonicalMessage{{Role: role, Blocks: []CanonicalBlock{{Type: BlockText, Text: text}}}}, nil
	}

	// Try as array of InputItems.
	var contentItems []openai.InputItem
	if err := json.Unmarshal(item.Content, &contentItems); err != nil {
		return nil, fmt.Errorf("invalid message content: %w", err)
	}

	var blocks []CanonicalBlock
	for _, ci := range contentItems {
		switch ci.Type {
		case "input_text":
			blocks = append(blocks, CanonicalBlock{Type: BlockText, Text: ci.Text})
		case "input_image":
			cb := CanonicalBlock{Type: BlockImage}
			if parseImageURL(ci.ImageURL, &cb) {
				blocks = append(blocks, cb)
			}
		default:
			blocks = append(blocks, CanonicalBlock{Type: BlockText, Text: ci.Text})
		}
	}

	return []CanonicalMessage{{Role: role, Blocks: blocks}}, nil
}

// parseImageURL populates a CanonicalBlock with image data from a URL. Returns false if empty.
func parseImageURL(url string, cb *CanonicalBlock) bool {
	if url == "" {
		return false
	}
	if len(url) > 5 && url[:5] == "data:" {
		parts := splitDataURI(url)
		if parts != nil {
			cb.ImageMediaType = parts[0]
			cb.ImageData = parts[1]
			return true
		}
	}
	cb.ImageURL = url
	return true
}

func splitDataURI(uri string) []string {
	withoutScheme := uri[5:] // strip "data:"
	idx := -1
	for i, c := range withoutScheme {
		if c == ',' {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	header := withoutScheme[:idx]
	data := withoutScheme[idx+1:]
	mediaType := header
	if len(mediaType) > 7 && mediaType[len(mediaType)-7:] == ";base64" {
		mediaType = mediaType[:len(mediaType)-7]
	}
	return []string{mediaType, data}
}

func buildDataURI(mediaType, data string) string {
	return "data:" + mediaType + ";base64," + data
}

func responsesToolChoiceToCanonical(tc any) (*CanonicalToolChoice, error) {
	switch v := tc.(type) {
	case string:
		switch v {
		case "none":
			return &CanonicalToolChoice{Mode: ToolChoiceNone}, nil
		case "auto":
			return &CanonicalToolChoice{Mode: ToolChoiceAuto}, nil
		case "required":
			return &CanonicalToolChoice{Mode: ToolChoiceRequired}, nil
		default:
			return nil, fmt.Errorf("unsupported tool_choice value: %s", v)
		}
	case map[string]any:
		if name, ok := v["name"].(string); ok {
			return &CanonicalToolChoice{Mode: ToolChoiceSpecific, FunctionName: name}, nil
		}
		return &CanonicalToolChoice{Mode: ToolChoiceAuto}, nil
	default:
		return nil, fmt.Errorf("unsupported tool_choice type")
	}
}

// CanonicalToResponsesRequest converts a canonical request to Responses API format.
func CanonicalToResponsesRequest(canon *CanonicalRequest, upstreamModel string) (*openai.ResponsesRequest, error) {
	if canon == nil {
		return nil, fmt.Errorf("canonical request must not be nil")
	}

	result := &openai.ResponsesRequest{
		Model:       upstreamModel,
		Temperature: canon.Temperature,
		TopP:        canon.TopP,
	}

	if canon.Stream {
		streaming := true
		result.Stream = &streaming
	}

	if canon.MaxTokens != nil {
		result.MaxOutputTokens = canon.MaxTokens
	}

	if canon.System != "" {
		result.Instructions = &canon.System
	}

	// Convert messages to input items.
	var items []openai.InputItem
	for _, msg := range canon.Messages {
		msgItems := canonicalMessageToResponsesInput(msg)
		items = append(items, msgItems...)
	}

	if len(items) > 0 {
		inputJSON, err := json.Marshal(items)
		if err != nil {
			return nil, fmt.Errorf("marshalling input: %w", err)
		}
		result.Input = inputJSON
	}

	// Convert tools.
	for _, t := range canon.Tools {
		var params json.RawMessage
		if t.Parameters != nil {
			p, err := json.Marshal(t.Parameters)
			if err != nil {
				return nil, fmt.Errorf("marshalling tool %q parameters: %w", t.Name, err)
			}
			params = p
		}
		result.Tools = append(result.Tools, openai.ResponsesTool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  params,
		})
	}

	// Convert tool_choice.
	if canon.ToolChoice != nil {
		result.ToolChoice = canonicalToolChoiceToResponses(canon.ToolChoice)
	}

	// Convert thinking.
	if canon.Thinking != nil && canon.Thinking.Enabled {
		effort := canon.Thinking.Effort
		if effort == "" && canon.Thinking.BudgetTokens > 0 {
			effort = budgetTokensToEffort(canon.Thinking.BudgetTokens)
		}
		if effort != "" {
			summary := "auto"
			result.Reasoning = &openai.Reasoning{
				Effort:  &effort,
				Summary: &summary,
			}
		}
	}

	return result, nil
}

func canonicalMessageToResponsesInput(msg CanonicalMessage) []openai.InputItem {
	var items []openai.InputItem

	for _, b := range msg.Blocks {
		switch b.Type {
		case BlockText:
			items = append(items, openai.InputItem{
				Type:    "message",
				Role:    msg.Role,
				Content: json.RawMessage(`"` + escapeJSON(b.Text) + `"`),
			})
		case BlockToolUse:
			items = append(items, openai.InputItem{
				Type:      "function_call",
				CallID:    b.ToolID,
				Name:      b.ToolName,
				Arguments: string(marshalToolInput(b.ToolInput)),
			})
		case BlockToolResult:
			items = append(items, openai.InputItem{
				Type:   "function_call_output",
				CallID: b.ToolUseID,
				Output: b.ToolResultText,
			})
		case BlockImage:
			url := b.ImageURL
			if b.ImageData != "" {
				url = buildDataURI(b.ImageMediaType, b.ImageData)
			}
			if url != "" {
				items = append(items, openai.InputItem{
					Type:     "input_image",
					ImageURL: url,
				})
			}
		case BlockDocument:
			fileData := b.DocURL
			if b.DocData != "" {
				fileData = buildDataURI(b.DocMediaType, b.DocData)
			}
			if fileData != "" {
				items = append(items, openai.InputItem{
					Type:     "input_file",
					FileData: fileData,
					Filename: b.DocFilename,
				})
			}
		}
	}

	return items
}

func canonicalToolChoiceToResponses(tc *CanonicalToolChoice) any {
	switch tc.Mode {
	case ToolChoiceNone:
		return "none"
	case ToolChoiceRequired:
		return "required"
	case ToolChoiceSpecific:
		return map[string]any{"type": "function", "name": tc.FunctionName}
	default:
		return "auto"
	}
}

// ResponsesResponseToCanonical converts a Responses API response to canonical form.
func ResponsesResponseToCanonical(resp *openai.ResponsesResponse) *CanonicalResponse {
	canon := &CanonicalResponse{
		ID:         resp.ID,
		Model:      resp.Model,
		StopReason: responsesStatusToCanonical(resp.Status, resp.Output),
	}

	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, content := range item.Content {
				switch content.Type {
				case "output_text":
					canon.Content = append(canon.Content, CanonicalBlock{Type: BlockText, Text: content.Text})
				case "refusal":
					canon.Content = append(canon.Content, CanonicalBlock{Type: BlockRefusal, RefusalText: content.Refusal})
				}
			}
		case "reasoning":
			for _, s := range item.Summary {
				canon.Content = append(canon.Content, CanonicalBlock{
					Type:         BlockThinking,
					ThinkingText: s.Text,
				})
			}
		case "function_call":
			canon.Content = append(canon.Content, CanonicalBlock{
				Type:      BlockToolUse,
				ToolID:    item.CallID,
				ToolName:  item.Name,
				ToolInput: unmarshalToolArgs(item.Arguments),
			})
		}
	}

	// Usage.
	if resp.Usage != nil {
		canon.Usage = CanonicalUsage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
		}
		if resp.Usage.InputTokensDetails != nil {
			canon.Usage.CacheReadInputTokens = resp.Usage.InputTokensDetails.CachedTokens
		}
		if resp.Usage.OutputTokensDetails != nil {
			canon.Usage.ReasoningTokens = resp.Usage.OutputTokensDetails.ReasoningTokens
		}
	}

	return canon
}

// CanonicalToResponsesResponse converts a canonical response to Responses API format.
func CanonicalToResponsesResponse(canon *CanonicalResponse, model string) *openai.ResponsesResponse {
	resp := &openai.ResponsesResponse{
		ID:        "resp_" + canon.ID,
		Object:    "response",
		CreatedAt: float64(time.Now().Unix()),
		Model:     model,
		Status:    canonicalStopReasonToResponsesStatus(canon.StopReason),
	}

	var output []openai.OutputItem
	var textContents []openai.OutputContent
	var reasoningItems []openai.OutputItem

	for _, b := range canon.Content {
		switch b.Type {
		case BlockText:
			textContents = append(textContents, openai.OutputContent{
				Type:        "output_text",
				Text:        b.Text,
				Annotations: []any{},
			})
		case BlockThinking:
			reasoningItems = append(reasoningItems, openai.OutputItem{
				Type: "reasoning",
				ID:   "rs_" + canon.ID,
				Summary: []openai.ReasoningSummary{{
					Type: "summary_text",
					Text: b.ThinkingText,
				}},
			})
		case BlockRefusal:
			textContents = append(textContents, openai.OutputContent{
				Type:    "refusal",
				Refusal: b.RefusalText,
			})
		case BlockToolUse:
			output = append(output, openai.OutputItem{
				Type:      "function_call",
				ID:        "fc_" + b.ToolID,
				CallID:    b.ToolID,
				Name:      b.ToolName,
				Arguments: string(marshalToolInput(b.ToolInput)),
				Status:    "completed",
			})
		}
	}

	// Prepend reasoning items.
	if len(reasoningItems) > 0 {
		output = append(reasoningItems, output...)
	}

	// Insert message after reasoning but before function calls.
	if len(textContents) > 0 {
		msgItem := openai.OutputItem{
			Type:    "message",
			ID:      "msg_" + canon.ID,
			Role:    "assistant",
			Content: textContents,
			Status:  "completed",
		}
		insertIdx := len(reasoningItems)
		output = slices.Insert(output, insertIdx, msgItem)
	}

	resp.Output = output

	// Usage. In canonical form, InputTokens may have CacheReadInputTokens subtracted.
	// Responses API InputTokens includes cached tokens.
	inputTokens := canon.Usage.InputTokens + canon.Usage.CacheReadInputTokens
	resp.Usage = &openai.ResponsesUsage{
		InputTokens:  inputTokens,
		OutputTokens: canon.Usage.OutputTokens,
		TotalTokens:  inputTokens + canon.Usage.OutputTokens,
	}
	if canon.Usage.CacheReadInputTokens > 0 {
		resp.Usage.InputTokensDetails = &openai.InputTokensDetails{
			CachedTokens: canon.Usage.CacheReadInputTokens,
		}
	}
	if canon.Usage.ReasoningTokens > 0 {
		resp.Usage.OutputTokensDetails = &openai.OutputTokensDetails{
			ReasoningTokens: canon.Usage.ReasoningTokens,
		}
	}

	return resp
}
