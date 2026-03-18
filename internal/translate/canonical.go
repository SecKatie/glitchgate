// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/seckatie/glitchgate/internal/provider/anthropic"
	"github.com/seckatie/glitchgate/internal/provider/gemini"
	"github.com/seckatie/glitchgate/internal/provider/openai"
)

// ---------------------------------------------------------------------------
// Canonical IR types
// ---------------------------------------------------------------------------

// StopReason enumerates the reasons a model stopped generating.
type StopReason int

// StopReason values enumerate why the model stopped generating.
const (
	StopReasonEndTurn StopReason = iota
	StopReasonMaxTokens
	StopReasonToolUse
	StopReasonStopSequence
	StopReasonContentFilter
	StopReasonUnset   // nil/missing stop reason
	StopReasonUnknown // unknown value; check RawStopReason
)

// ToolChoiceMode enumerates how the model should select tools.
type ToolChoiceMode int

// ToolChoiceMode values enumerate how the model should select tools.
const (
	ToolChoiceAuto ToolChoiceMode = iota
	ToolChoiceRequired
	ToolChoiceNone
	ToolChoiceSpecific
)

// CanonicalToolChoice describes the tool selection policy.
type CanonicalToolChoice struct {
	Mode         ToolChoiceMode
	FunctionName string // only used when Mode == ToolChoiceSpecific
}

// CanonicalTool describes a function tool available to the model.
type CanonicalTool struct {
	Name        string
	Description string
	Parameters  any // JSON Schema as map[string]any
}

// BlockType tags the kind of content within a CanonicalBlock.
type BlockType int

// BlockType values tag the kind of content within a CanonicalBlock.
const (
	BlockText BlockType = iota
	BlockImage
	BlockToolUse
	BlockToolResult
	BlockThinking
	BlockRefusal
	BlockAudio
	BlockDocument
)

// CanonicalBlock is a tagged union representing one segment of message content.
type CanonicalBlock struct {
	Type BlockType

	// BlockText
	Text string

	// BlockImage
	ImageMediaType string // e.g. "image/png"
	ImageData      string // base64-encoded data
	ImageURL       string // plain URL (mutually exclusive with ImageData)

	// BlockToolUse
	ToolID    string
	ToolName  string
	ToolInput any // parsed JSON (map[string]any)

	// BlockToolResult
	ToolUseID      string
	ToolResultText string

	// BlockThinking
	ThinkingText string

	// BlockRefusal
	RefusalText string

	// BlockAudio
	AudioData   string // base64-encoded audio data
	AudioFormat string // e.g. "wav", "mp3"

	// BlockDocument
	DocMediaType string // e.g. "application/pdf"
	DocData      string // base64-encoded document data
	DocURL       string // URL (mutually exclusive with DocData)
	DocFilename  string // optional original filename
}

// CanonicalMessage represents a single conversational turn.
type CanonicalMessage struct {
	Role   string // "system", "user", "assistant", "tool"
	Blocks []CanonicalBlock
}

// CanonicalUsage reports token consumption in a format-neutral way.
type CanonicalUsage struct {
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	ReasoningTokens          int64
}

// CanonicalThinking holds reasoning/thinking configuration.
type CanonicalThinking struct {
	Enabled      bool
	BudgetTokens int    // explicit budget (Anthropic style)
	Effort       string // "low", "medium", "high" (OpenAI style)
}

// CanonicalRequest is the format-neutral intermediate representation for LLM requests.
type CanonicalRequest struct {
	Model         string
	System        string
	Messages      []CanonicalMessage
	MaxTokens     *int
	Temperature   *float64
	TopP          *float64
	TopK          *int
	StopSequences []string
	Stream        bool
	Tools         []CanonicalTool
	ToolChoice    *CanonicalToolChoice
	Thinking      *CanonicalThinking
}

// CanonicalResponse is the format-neutral intermediate representation for LLM responses.
type CanonicalResponse struct {
	ID            string
	Model         string
	StopReason    StopReason
	RawStopReason *string // original stop reason string for pass-through of unknown values
	Content       []CanonicalBlock
	Usage         CanonicalUsage
}

// ---------------------------------------------------------------------------
// Shared stop-reason mappings
// ---------------------------------------------------------------------------

func canonicalStopReasonToAnthropic(sr StopReason) string {
	switch sr {
	case StopReasonMaxTokens:
		return "max_tokens"
	case StopReasonToolUse:
		return "tool_use"
	case StopReasonStopSequence:
		return "stop_sequence"
	default:
		return "end_turn"
	}
}

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

// canonicalStopReasonToOpenAIFinish maps canonical stop reason to OpenAI finish_reason.
func canonicalStopReasonToOpenAIFinish(sr StopReason) *string {
	switch sr {
	case StopReasonUnset:
		return nil
	case StopReasonMaxTokens:
		s := "length"
		return &s
	case StopReasonToolUse:
		s := "tool_calls"
		return &s
	case StopReasonContentFilter:
		s := "content_filter"
		return &s
	default:
		s := "stop"
		return &s
	}
}

// openAIFinishToCanonical maps OpenAI finish_reason to canonical.
func openAIFinishToCanonical(finishReason *string) StopReason {
	if finishReason == nil {
		return StopReasonUnset
	}
	switch *finishReason {
	case "length":
		return StopReasonMaxTokens
	case "tool_calls":
		return StopReasonToolUse
	case "content_filter":
		return StopReasonContentFilter
	default:
		return StopReasonEndTurn
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

func anthropicStopReasonToCanonical(stopReason *string) (StopReason, *string) {
	if stopReason == nil {
		return StopReasonUnset, nil
	}
	switch *stopReason {
	case "max_tokens":
		return StopReasonMaxTokens, nil
	case "tool_use":
		return StopReasonToolUse, nil
	case "stop_sequence":
		return StopReasonStopSequence, nil
	case "end_turn":
		return StopReasonEndTurn, nil
	default:
		return StopReasonUnknown, stopReason
	}
}

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

// ---------------------------------------------------------------------------
// Anthropic ↔ Canonical
// ---------------------------------------------------------------------------

// AnthropicRequestToCanonical converts an Anthropic MessagesRequest to the
// canonical intermediate representation.
func AnthropicRequestToCanonical(req *anthropic.MessagesRequest) (*CanonicalRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request must not be nil")
	}

	canon := &CanonicalRequest{
		Model:         req.Model,
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		TopK:          req.TopK,
		StopSequences: req.StopSequences,
		Stream:        req.Stream,
	}

	if req.MaxTokens > 0 {
		mt := req.MaxTokens
		canon.MaxTokens = &mt
	}

	// Extract system text.
	if req.System != nil {
		canon.System = extractAnthropicSystem(req.System)
	}

	// Convert messages.
	for _, msg := range req.Messages {
		canonMsg, err := anthropicMessageToCanonical(msg)
		if err != nil {
			return nil, fmt.Errorf("translating %s message: %w", msg.Role, err)
		}
		canon.Messages = append(canon.Messages, canonMsg)
	}

	// Convert tools.
	for _, t := range req.Tools {
		canon.Tools = append(canon.Tools, CanonicalTool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.InputSchema,
		})
	}

	// Convert tool_choice.
	if req.ToolChoice != nil {
		tc, err := anthropicToolChoiceToCanonical(req.ToolChoice)
		if err != nil {
			return nil, err
		}
		canon.ToolChoice = tc
	}

	// Convert thinking config.
	if req.Thinking != nil && req.Thinking.Type == "enabled" {
		canon.Thinking = &CanonicalThinking{
			Enabled:      true,
			BudgetTokens: req.Thinking.BudgetTokens,
			Effort:       budgetTokensToEffort(req.Thinking.BudgetTokens),
		}
	}

	return canon, nil
}

// anthropicMessageToCanonical converts a single Anthropic message.
func anthropicMessageToCanonical(msg anthropic.Message) (CanonicalMessage, error) {
	canonMsg := CanonicalMessage{Role: msg.Role}

	// String content → single text block.
	if text, ok := msg.Content.(string); ok {
		canonMsg.Blocks = []CanonicalBlock{{Type: BlockText, Text: text}}
		return canonMsg, nil
	}

	// Array content → parse as content blocks.
	blocks, err := parseContentBlocks(msg.Content)
	if err != nil {
		return canonMsg, err
	}

	for _, b := range blocks {
		switch b.Type {
		case "text":
			canonMsg.Blocks = append(canonMsg.Blocks, CanonicalBlock{Type: BlockText, Text: b.Text})
		case "image":
			cb := CanonicalBlock{Type: BlockImage}
			if b.Source != nil {
				if b.Source.Type == "base64" {
					cb.ImageMediaType = b.Source.MediaType
					cb.ImageData = b.Source.Data
				} else {
					cb.ImageURL = b.Source.URL
				}
			}
			canonMsg.Blocks = append(canonMsg.Blocks, cb)
		case "document":
			cb := CanonicalBlock{Type: BlockDocument}
			if b.Source != nil {
				if b.Source.Type == "base64" {
					cb.DocMediaType = b.Source.MediaType
					cb.DocData = b.Source.Data
				} else {
					cb.DocURL = b.Source.URL
				}
			}
			canonMsg.Blocks = append(canonMsg.Blocks, cb)
		case "tool_use":
			canonMsg.Blocks = append(canonMsg.Blocks, CanonicalBlock{
				Type:      BlockToolUse,
				ToolID:    b.ID,
				ToolName:  b.Name,
				ToolInput: b.Input,
			})
		case "tool_result":
			canonMsg.Blocks = append(canonMsg.Blocks, CanonicalBlock{
				Type:           BlockToolResult,
				ToolUseID:      b.ToolUseID,
				ToolResultText: extractToolResultText(b),
			})
		case "thinking":
			canonMsg.Blocks = append(canonMsg.Blocks, CanonicalBlock{
				Type:         BlockThinking,
				ThinkingText: b.Thinking,
			})
		}
	}

	return canonMsg, nil
}

// anthropicToolChoiceToCanonical converts the Anthropic tool_choice interface to canonical.
func anthropicToolChoiceToCanonical(tc any) (*CanonicalToolChoice, error) {
	switch v := tc.(type) {
	case map[string]string:
		return anthropicToolChoiceMapToCanonical(v["type"], v["name"]), nil
	case map[string]any:
		t, _ := v["type"].(string)
		name, _ := v["name"].(string)
		return anthropicToolChoiceMapToCanonical(t, name), nil
	default:
		return nil, fmt.Errorf("unsupported tool_choice type")
	}
}

func anthropicToolChoiceMapToCanonical(typ, name string) *CanonicalToolChoice {
	switch typ {
	case "any":
		return &CanonicalToolChoice{Mode: ToolChoiceRequired}
	case "tool":
		return &CanonicalToolChoice{Mode: ToolChoiceSpecific, FunctionName: name}
	default:
		return &CanonicalToolChoice{Mode: ToolChoiceAuto}
	}
}

// CanonicalToAnthropicRequest converts a canonical request to Anthropic format.
func CanonicalToAnthropicRequest(canon *CanonicalRequest) (*anthropic.MessagesRequest, error) {
	if canon == nil {
		return nil, fmt.Errorf("canonical request must not be nil")
	}

	result := &anthropic.MessagesRequest{
		Model:         canon.Model,
		Temperature:   canon.Temperature,
		TopP:          canon.TopP,
		TopK:          canon.TopK,
		StopSequences: canon.StopSequences,
		Stream:        canon.Stream,
	}

	if canon.MaxTokens != nil {
		result.MaxTokens = *canon.MaxTokens
	} else {
		result.MaxTokens = defaultMaxTokens
	}

	if canon.System != "" {
		result.System = canon.System
	}

	// Convert messages.
	for _, msg := range canon.Messages {
		// Validate: Anthropic does not support audio content.
		for _, b := range msg.Blocks {
			if b.Type == BlockAudio {
				return nil, fmt.Errorf("input_audio content type is not supported by Anthropic upstream")
			}
		}
		anthMsg := canonicalMessageToAnthropic(msg)
		result.Messages = append(result.Messages, anthMsg)
	}

	// Convert tools.
	for _, t := range canon.Tools {
		schema := t.Parameters
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		result.Tools = append(result.Tools, anthropic.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}

	// Convert tool_choice.
	if canon.ToolChoice != nil {
		result.ToolChoice = canonicalToolChoiceToAnthropic(canon.ToolChoice)
	}

	// Convert thinking.
	if canon.Thinking != nil && canon.Thinking.Enabled {
		budget := canon.Thinking.BudgetTokens
		if budget == 0 && canon.Thinking.Effort != "" {
			budget = effortToBudgetTokens(canon.Thinking.Effort, result.MaxTokens)
		}
		if budget > 0 {
			result.Thinking = &anthropic.ThinkingConfig{
				Type:         "enabled",
				BudgetTokens: budget,
			}
		}
	}

	return result, nil
}

// canonicalMessageToAnthropic converts a canonical message to an Anthropic message.
func canonicalMessageToAnthropic(msg CanonicalMessage) anthropic.Message {
	anthMsg := anthropic.Message{Role: msg.Role}

	// Optimization: single text block → string content.
	if len(msg.Blocks) == 1 && msg.Blocks[0].Type == BlockText {
		anthMsg.Content = msg.Blocks[0].Text
		return anthMsg
	}

	var blocks []anthropic.ContentBlock
	for _, b := range msg.Blocks {
		switch b.Type {
		case BlockText:
			blocks = append(blocks, anthropic.ContentBlock{Type: "text", Text: b.Text})
		case BlockImage:
			cb := anthropic.ContentBlock{Type: "image"}
			if b.ImageData != "" {
				cb.Source = &anthropic.ImageSource{
					Type:      "base64",
					MediaType: b.ImageMediaType,
					Data:      b.ImageData,
				}
			} else if b.ImageURL != "" {
				cb.Source = &anthropic.ImageSource{
					Type: "url",
					URL:  b.ImageURL,
				}
			}
			blocks = append(blocks, cb)
		case BlockDocument:
			cb := anthropic.ContentBlock{Type: "document"}
			if b.DocData != "" {
				cb.Source = &anthropic.ImageSource{
					Type:      "base64",
					MediaType: b.DocMediaType,
					Data:      b.DocData,
				}
			} else if b.DocURL != "" {
				cb.Source = &anthropic.ImageSource{
					Type: "url",
					URL:  b.DocURL,
				}
			}
			blocks = append(blocks, cb)
		case BlockToolUse:
			blocks = append(blocks, anthropic.ContentBlock{
				Type:  "tool_use",
				ID:    sanitizeAnthropicToolID(b.ToolID),
				Name:  b.ToolName,
				Input: b.ToolInput,
			})
		case BlockToolResult:
			blocks = append(blocks, anthropic.ContentBlock{
				Type:      "tool_result",
				ToolUseID: sanitizeAnthropicToolID(b.ToolUseID),
				Content:   b.ToolResultText,
			})
		case BlockThinking:
			blocks = append(blocks, anthropic.ContentBlock{
				Type:     "thinking",
				Thinking: b.ThinkingText,
			})
		}
	}

	anthMsg.Content = blocks
	return anthMsg
}

// canonicalToolChoiceToAnthropic converts canonical tool choice to Anthropic format.
func canonicalToolChoiceToAnthropic(tc *CanonicalToolChoice) any {
	switch tc.Mode {
	case ToolChoiceRequired:
		return map[string]string{"type": "any"}
	case ToolChoiceSpecific:
		return map[string]string{"type": "tool", "name": tc.FunctionName}
	case ToolChoiceNone:
		return nil
	default:
		return map[string]string{"type": "auto"}
	}
}

// AnthropicResponseToCanonical converts an Anthropic response to canonical form.
func AnthropicResponseToCanonical(resp *anthropic.MessagesResponse) *CanonicalResponse {
	canon := &CanonicalResponse{
		ID:    resp.ID,
		Model: resp.Model,
		Usage: CanonicalUsage{
			InputTokens:              resp.Usage.InputTokens,
			OutputTokens:             resp.Usage.OutputTokens,
			CacheCreationInputTokens: resp.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     resp.Usage.CacheReadInputTokens,
		},
	}

	// Map stop reason.
	canon.StopReason, canon.RawStopReason = anthropicStopReasonToCanonical(resp.StopReason)

	// Convert content blocks.
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			canon.Content = append(canon.Content, CanonicalBlock{Type: BlockText, Text: b.Text})
		case "thinking":
			canon.Content = append(canon.Content, CanonicalBlock{Type: BlockThinking, ThinkingText: b.Thinking})
		case "tool_use":
			canon.Content = append(canon.Content, CanonicalBlock{
				Type:      BlockToolUse,
				ToolID:    b.ID,
				ToolName:  b.Name,
				ToolInput: b.Input,
			})
		}
	}

	return canon
}

// CanonicalToAnthropicResponse converts a canonical response to Anthropic format.
func CanonicalToAnthropicResponse(canon *CanonicalResponse, model string) *anthropic.MessagesResponse {
	resp := &anthropic.MessagesResponse{
		ID:    canon.ID,
		Type:  "message",
		Role:  "assistant",
		Model: model,
		Usage: anthropic.Usage{
			InputTokens:              canon.Usage.InputTokens,
			OutputTokens:             canon.Usage.OutputTokens,
			CacheCreationInputTokens: canon.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     canon.Usage.CacheReadInputTokens,
		},
	}

	// Convert blocks.
	for _, b := range canon.Content {
		switch b.Type {
		case BlockText:
			resp.Content = append(resp.Content, anthropic.ContentBlock{Type: "text", Text: b.Text})
		case BlockThinking:
			resp.Content = append(resp.Content, anthropic.ContentBlock{Type: "thinking", Thinking: b.ThinkingText})
		case BlockToolUse:
			resp.Content = append(resp.Content, anthropic.ContentBlock{
				Type:  "tool_use",
				ID:    sanitizeAnthropicToolID(b.ToolID),
				Name:  b.ToolName,
				Input: b.ToolInput,
			})
		case BlockRefusal:
			// Anthropic has no refusal block; emit as text.
			resp.Content = append(resp.Content, anthropic.ContentBlock{Type: "text", Text: b.RefusalText})
		}
	}

	// Map stop reason.
	if canon.StopReason == StopReasonUnset {
		resp.StopReason = nil
	} else if canon.StopReason == StopReasonUnknown && canon.RawStopReason != nil {
		resp.StopReason = canon.RawStopReason
	} else {
		reason := canonicalStopReasonToAnthropic(canon.StopReason)
		resp.StopReason = &reason
	}

	return resp
}

// ---------------------------------------------------------------------------
// OpenAI ↔ Canonical
// ---------------------------------------------------------------------------

// OpenAIRequestToCanonical converts a openai.ChatCompletionRequest to canonical form.
func OpenAIRequestToCanonical(req *openai.ChatCompletionRequest) (*CanonicalRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request must not be nil")
	}

	canon := &CanonicalRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
	}

	if req.MaxTokens != nil {
		canon.MaxTokens = req.MaxTokens
	}

	// Parse stop sequences.
	if req.Stop != nil {
		seqs, err := parseStopSequences(req.Stop)
		if err != nil {
			return nil, fmt.Errorf("invalid stop field: %w", err)
		}
		canon.StopSequences = seqs
	}

	// Extract system messages and convert other messages.
	var systemParts []string
	for _, msg := range req.Messages {
		switch msg.Role {
		case "system":
			text, err := extractTextContent(msg.Content)
			if err != nil {
				return nil, fmt.Errorf("invalid system message content: %w", err)
			}
			systemParts = append(systemParts, text)

		case "user":
			canonMsg, err := openAIUserMessageToCanonical(msg)
			if err != nil {
				return nil, fmt.Errorf("invalid user message: %w", err)
			}
			canon.Messages = append(canon.Messages, canonMsg)

		case "assistant":
			canonMsg := openAIAssistantMessageToCanonical(msg)
			canon.Messages = append(canon.Messages, canonMsg)

		case "tool":
			text, err := extractTextContent(msg.Content)
			if err != nil {
				return nil, fmt.Errorf("invalid tool message content: %w", err)
			}
			canon.Messages = append(canon.Messages, CanonicalMessage{
				Role: "user",
				Blocks: []CanonicalBlock{{
					Type:           BlockToolResult,
					ToolUseID:      msg.ToolCallID,
					ToolResultText: text,
				}},
			})

		default:
			return nil, fmt.Errorf("unsupported message role: %s", msg.Role)
		}
	}

	if len(systemParts) > 0 {
		canon.System = strings.Join(systemParts, "\n")
	}

	// Convert tools.
	for _, t := range req.Tools {
		if t.Type != "function" {
			continue
		}
		canon.Tools = append(canon.Tools, CanonicalTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}

	// Convert tool_choice.
	if req.ToolChoice != nil {
		tc, err := openAIToolChoiceToCanonical(req.ToolChoice)
		if err != nil {
			return nil, fmt.Errorf("invalid tool_choice: %w", err)
		}
		canon.ToolChoice = tc
	}

	// Convert reasoning_effort.
	if req.ReasoningEffort != nil && *req.ReasoningEffort != "" {
		canon.Thinking = &CanonicalThinking{
			Enabled: true,
			Effort:  *req.ReasoningEffort,
		}
	}

	return canon, nil
}

func openAIUserMessageToCanonical(msg openai.ChatMessage) (CanonicalMessage, error) {
	canonMsg := CanonicalMessage{Role: "user"}

	switch v := msg.Content.(type) {
	case string:
		canonMsg.Blocks = []CanonicalBlock{{Type: BlockText, Text: v}}
	case []any:
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			t, _ := m["type"].(string)
			switch t {
			case "text":
				if text, ok := m["text"].(string); ok {
					canonMsg.Blocks = append(canonMsg.Blocks, CanonicalBlock{Type: BlockText, Text: text})
				}
			case "image_url":
				imgURL, ok := m["image_url"].(map[string]any)
				if !ok {
					continue
				}
				url, _ := imgURL["url"].(string)
				if url == "" {
					continue
				}
				cb := CanonicalBlock{Type: BlockImage}
				if strings.HasPrefix(url, "data:") {
					parts := strings.SplitN(url, ",", 2)
					if len(parts) == 2 {
						mediaInfo := strings.TrimPrefix(parts[0], "data:")
						mediaInfo = strings.TrimSuffix(mediaInfo, ";base64")
						cb.ImageMediaType = mediaInfo
						cb.ImageData = parts[1]
					}
				} else {
					cb.ImageURL = url
				}
				canonMsg.Blocks = append(canonMsg.Blocks, cb)
			case "file":
				fileObj, ok := m["file"].(map[string]any)
				if !ok {
					continue
				}
				fileData, _ := fileObj["file_data"].(string)
				if fileData == "" {
					continue
				}
				cb := CanonicalBlock{Type: BlockDocument}
				if fn, ok := fileObj["filename"].(string); ok {
					cb.DocFilename = fn
				}
				// Parse data URI: "data:application/pdf;base64,<data>"
				if strings.HasPrefix(fileData, "data:") {
					parts := strings.SplitN(fileData, ",", 2)
					if len(parts) == 2 {
						mediaInfo := strings.TrimPrefix(parts[0], "data:")
						mediaInfo = strings.TrimSuffix(mediaInfo, ";base64")
						cb.DocMediaType = mediaInfo
						cb.DocData = parts[1]
					}
				}
				canonMsg.Blocks = append(canonMsg.Blocks, cb)
			}
		}
	case nil:
		canonMsg.Blocks = []CanonicalBlock{{Type: BlockText, Text: ""}}
	default:
		text, err := extractTextContent(msg.Content)
		if err != nil {
			return canonMsg, err
		}
		canonMsg.Blocks = []CanonicalBlock{{Type: BlockText, Text: text}}
	}

	return canonMsg, nil
}

func openAIAssistantMessageToCanonical(msg openai.ChatMessage) CanonicalMessage {
	canonMsg := CanonicalMessage{Role: "assistant"}

	// Reasoning/thinking content.
	reasoning := msg.Reasoning
	if reasoning == "" {
		reasoning = msg.ReasoningContent
	}
	if reasoning != "" {
		canonMsg.Blocks = append(canonMsg.Blocks, CanonicalBlock{
			Type:         BlockThinking,
			ThinkingText: reasoning,
		})
	}

	// Text content.
	text, _ := extractTextContent(msg.Content)
	if text != "" {
		canonMsg.Blocks = append(canonMsg.Blocks, CanonicalBlock{Type: BlockText, Text: text})
	}

	// Refusal.
	if msg.Refusal != "" {
		canonMsg.Blocks = append(canonMsg.Blocks, CanonicalBlock{
			Type:        BlockRefusal,
			RefusalText: msg.Refusal,
		})
	}

	// Tool calls.
	for _, tc := range msg.ToolCalls {
		canonMsg.Blocks = append(canonMsg.Blocks, CanonicalBlock{
			Type:      BlockToolUse,
			ToolID:    tc.ID,
			ToolName:  tc.Function.Name,
			ToolInput: unmarshalToolArgs(tc.Function.Arguments),
		})
	}

	return canonMsg
}

func openAIToolChoiceToCanonical(tc any) (*CanonicalToolChoice, error) {
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
			return nil, fmt.Errorf("unknown tool_choice string: %s", v)
		}
	case map[string]any:
		if fn, ok := v["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok {
				return &CanonicalToolChoice{Mode: ToolChoiceSpecific, FunctionName: name}, nil
			}
		}
		return &CanonicalToolChoice{Mode: ToolChoiceAuto}, nil
	default:
		return nil, fmt.Errorf("unsupported tool_choice type")
	}
}

// CanonicalToOpenAIRequest converts a canonical request to OpenAI Chat Completions format.
func CanonicalToOpenAIRequest(canon *CanonicalRequest) (*openai.ChatCompletionRequest, error) {
	if canon == nil {
		return nil, fmt.Errorf("canonical request must not be nil")
	}

	result := &openai.ChatCompletionRequest{
		Model:       canon.Model,
		Temperature: canon.Temperature,
		TopP:        canon.TopP,
		Stream:      canon.Stream,
	}

	if canon.MaxTokens != nil {
		result.MaxTokens = canon.MaxTokens
	}

	if len(canon.StopSequences) > 0 {
		result.Stop = canon.StopSequences
	}

	var messages []openai.ChatMessage

	// System message.
	if canon.System != "" {
		messages = append(messages, openai.ChatMessage{
			Role:    "system",
			Content: canon.System,
		})
	}

	// Convert messages.
	for _, msg := range canon.Messages {
		oaiMsgs := canonicalMessageToOpenAI(msg)
		messages = append(messages, oaiMsgs...)
	}

	result.Messages = messages

	// Convert tools.
	for _, t := range canon.Tools {
		result.Tools = append(result.Tools, openai.Tool{
			Type: "function",
			Function: openai.ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	// Convert tool_choice.
	if canon.ToolChoice != nil {
		result.ToolChoice = canonicalToolChoiceToOpenAI(canon.ToolChoice)
	}

	// Convert thinking to reasoning_effort.
	if canon.Thinking != nil && canon.Thinking.Enabled {
		effort := canon.Thinking.Effort
		if effort == "" && canon.Thinking.BudgetTokens > 0 {
			effort = budgetTokensToEffort(canon.Thinking.BudgetTokens)
		}
		if effort != "" {
			result.ReasoningEffort = &effort
		}
	}

	// Request stream_options.include_usage for streaming.
	if result.Stream {
		result.StreamOptions = &openai.StreamOptions{IncludeUsage: true}
	}

	return result, nil
}

// canonicalMessageToOpenAI converts a canonical message to one or more OpenAI messages.
// Tool result blocks become separate "tool" role messages.
func canonicalMessageToOpenAI(msg CanonicalMessage) []openai.ChatMessage {
	if msg.Role == "user" {
		return canonicalUserToOpenAI(msg)
	}

	// Assistant message.
	oaiMsg := openai.ChatMessage{Role: "assistant"}
	var textParts []string

	for _, b := range msg.Blocks {
		switch b.Type {
		case BlockText:
			textParts = append(textParts, b.Text)
		case BlockThinking:
			oaiMsg.ReasoningContent = b.ThinkingText
		case BlockRefusal:
			oaiMsg.Refusal = b.RefusalText
		case BlockToolUse:
			oaiMsg.ToolCalls = append(oaiMsg.ToolCalls, openai.ToolCall{
				ID:   b.ToolID,
				Type: "function",
				Function: openai.FunctionCall{
					Name:      b.ToolName,
					Arguments: string(marshalToolInput(b.ToolInput)),
				},
			})
		}
	}

	if len(textParts) > 0 {
		oaiMsg.Content = strings.Join(textParts, "")
	}

	return []openai.ChatMessage{oaiMsg}
}

// canonicalUserToOpenAI handles user messages, splitting tool results into separate messages.
func canonicalUserToOpenAI(msg CanonicalMessage) []openai.ChatMessage {
	var messages []openai.ChatMessage
	var parts []openai.ContentPart
	hasImages := false

	flushParts := func() {
		if len(parts) == 0 {
			return
		}
		if hasImages {
			messages = append(messages, openai.ChatMessage{Role: "user", Content: parts})
		} else {
			var texts []string
			for _, p := range parts {
				texts = append(texts, p.Text)
			}
			messages = append(messages, openai.ChatMessage{Role: "user", Content: strings.Join(texts, "")})
		}
		parts = nil
		hasImages = false
	}

	for _, b := range msg.Blocks {
		switch b.Type {
		case BlockText:
			parts = append(parts, openai.ContentPart{Type: "text", Text: b.Text})
		case BlockImage:
			url := b.ImageURL
			if b.ImageData != "" {
				url = "data:" + b.ImageMediaType + ";base64," + b.ImageData
			}
			if url != "" {
				parts = append(parts, openai.ContentPart{
					Type:     "image_url",
					ImageURL: &openai.ImageURLContent{URL: url},
				})
				hasImages = true
			}
		case BlockAudio:
			parts = append(parts, openai.ContentPart{
				Type: "input_audio",
				InputAudio: &openai.InputAudioContent{
					Data:   b.AudioData,
					Format: b.AudioFormat,
				},
			})
			hasImages = true // force multipart content
		case BlockDocument:
			fileData := b.DocURL
			if b.DocData != "" {
				fileData = "data:" + b.DocMediaType + ";base64," + b.DocData
			}
			if fileData != "" {
				parts = append(parts, openai.ContentPart{
					Type: "file",
					File: &openai.FileContent{
						FileData: fileData,
						Filename: b.DocFilename,
					},
				})
				hasImages = true // force multipart content
			}
		case BlockToolResult:
			flushParts()
			messages = append(messages, openai.ChatMessage{
				Role:       "tool",
				ToolCallID: b.ToolUseID,
				Content:    b.ToolResultText,
			})
		}
	}

	flushParts()
	return messages
}

func canonicalToolChoiceToOpenAI(tc *CanonicalToolChoice) any {
	switch tc.Mode {
	case ToolChoiceNone:
		return "none"
	case ToolChoiceRequired:
		return "required"
	case ToolChoiceSpecific:
		return map[string]any{
			"type": "function",
			"function": map[string]string{
				"name": tc.FunctionName,
			},
		}
	default:
		return "auto"
	}
}

// OpenAIResponseToCanonical converts a openai.ChatCompletionResponse to canonical form.
func OpenAIResponseToCanonical(resp *openai.ChatCompletionResponse) *CanonicalResponse {
	canon := &CanonicalResponse{
		ID:    strings.TrimPrefix(resp.ID, "chatcmpl-"),
		Model: resp.Model,
	}

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		canon.StopReason = openAIFinishToCanonical(choice.FinishReason)

		if msg := choice.Message; msg != nil {
			// Reasoning/thinking.
			reasoning := msg.Reasoning
			if reasoning == "" {
				reasoning = msg.ReasoningContent
			}
			if reasoning != "" {
				canon.Content = append(canon.Content, CanonicalBlock{
					Type:         BlockThinking,
					ThinkingText: reasoning,
				})
			}

			// Text content.
			switch c := msg.Content.(type) {
			case string:
				if c != "" {
					canon.Content = append(canon.Content, CanonicalBlock{Type: BlockText, Text: c})
				}
			case []any:
				for _, part := range c {
					if m, ok := part.(map[string]any); ok {
						if t, ok := m["text"].(string); ok && t != "" {
							canon.Content = append(canon.Content, CanonicalBlock{Type: BlockText, Text: t})
						}
					}
				}
			}

			// Refusal.
			if msg.Refusal != "" {
				canon.Content = append(canon.Content, CanonicalBlock{
					Type:        BlockRefusal,
					RefusalText: msg.Refusal,
				})
			}

			// Tool calls.
			for _, tc := range msg.ToolCalls {
				canon.Content = append(canon.Content, CanonicalBlock{
					Type:      BlockToolUse,
					ToolID:    tc.ID,
					ToolName:  tc.Function.Name,
					ToolInput: unmarshalToolArgs(tc.Function.Arguments),
				})
			}
		}
	}

	// Usage.
	if resp.Usage != nil {
		canon.Usage = CanonicalUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		}
		if resp.Usage.PromptTokensDetails != nil {
			canon.Usage.CacheReadInputTokens = resp.Usage.PromptTokensDetails.CachedTokens
		}
		if resp.Usage.CompletionTokensDetails != nil {
			canon.Usage.ReasoningTokens = resp.Usage.CompletionTokensDetails.ReasoningTokens
		}
	}

	return canon
}

// CanonicalToOpenAIResponse converts a canonical response to OpenAI format.
func CanonicalToOpenAIResponse(canon *CanonicalResponse, model string) *openai.ChatCompletionResponse {
	msg := &openai.ChatMessage{Role: "assistant"}

	var textParts []string
	var thinkingParts []string
	var toolCalls []openai.ToolCall

	for _, b := range canon.Content {
		switch b.Type {
		case BlockText:
			textParts = append(textParts, b.Text)
		case BlockThinking:
			thinkingParts = append(thinkingParts, b.ThinkingText)
		case BlockRefusal:
			msg.Refusal = b.RefusalText
		case BlockToolUse:
			toolCalls = append(toolCalls, openai.ToolCall{
				ID:   b.ToolID,
				Type: "function",
				Function: openai.FunctionCall{
					Name:      b.ToolName,
					Arguments: string(marshalToolInput(b.ToolInput)),
				},
			})
		}
	}

	if len(textParts) > 0 {
		msg.Content = strings.Join(textParts, "")
	}
	if len(thinkingParts) > 0 {
		msg.ReasoningContent = strings.Join(thinkingParts, "\n\n")
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}

	finishReason := canonicalStopReasonToOpenAIFinish(canon.StopReason)
	if canon.StopReason == StopReasonUnknown && canon.RawStopReason != nil {
		finishReason = canon.RawStopReason
	}

	result := &openai.ChatCompletionResponse{
		ID:      "chatcmpl-" + canon.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []openai.Choice{{
			Index:        0,
			Message:      msg,
			FinishReason: finishReason,
		}},
	}

	// Usage.
	// In canonical form, InputTokens may have CacheReadInputTokens subtracted out
	// (Anthropic convention). OpenAI PromptTokens includes cached tokens.
	promptTokens := canon.Usage.InputTokens + canon.Usage.CacheReadInputTokens
	result.Usage = &openai.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: canon.Usage.OutputTokens,
		TotalTokens:      promptTokens + canon.Usage.OutputTokens,
	}
	if canon.Usage.CacheReadInputTokens > 0 {
		result.Usage.PromptTokensDetails = &openai.PromptTokensDetails{
			CachedTokens: canon.Usage.CacheReadInputTokens,
		}
	}
	if canon.Usage.ReasoningTokens > 0 {
		result.Usage.CompletionTokensDetails = &openai.CompletionTokensDetails{
			ReasoningTokens: canon.Usage.ReasoningTokens,
		}
	}

	return result
}

// ---------------------------------------------------------------------------
// Responses ↔ Canonical
// ---------------------------------------------------------------------------

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
				url = "data:" + b.ImageMediaType + ";base64," + b.ImageData
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
				fileData = "data:" + b.DocMediaType + ";base64," + b.DocData
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

// ---------------------------------------------------------------------------
// Gemini ↔ Canonical
// ---------------------------------------------------------------------------

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
