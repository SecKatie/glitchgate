// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"sort"
	"strings"

	"codeberg.org/kglitchy/glitchgate/internal/provider/anthropic"
	"codeberg.org/kglitchy/glitchgate/internal/store"
)

// ConversationData holds the parsed view of a request body's conversation.
// Passed to log_detail.html as .Conversation.
type ConversationData struct {
	SystemPrompt    string // normalised from MessagesRequest.System; empty if absent
	SystemPromptLen int    // length in runes; 0 if no system prompt
	HasSystem       bool   // true if a system prompt is present

	LatestPrompt *ConvTurn  // the last user-role message; nil if messages is empty
	Response     *ConvTurn  // parsed from ResponseBody; nil if parse failed
	History      []ConvTurn // all turns before LatestPrompt, oldest first

	ParseFailed bool   // true if RequestBody could not be parsed as MessagesRequest
	RawRequest  string // pretty-printed RequestBody (always populated)
	RawResponse string // pretty-printed ResponseBody (always populated)
}

// ConvTurn is a single conversation turn for template rendering.
type ConvTurn struct {
	Role   string // "user", "assistant", "system"
	Blocks []ConvBlock
}

// ConvBlock is a typed content block within a turn.
type ConvBlock struct {
	// Type is one of: "text", "tool_use", "tool_result", "image", "document", "unknown"
	Type string

	// For Type="text"
	Text      string
	Truncated bool   // true if Text was truncated to ~500 chars
	FullText  string // complete text when Truncated=true

	// For Type="tool_use"
	ToolName  string    // e.g. "get_weather"
	ToolInput string    // full pretty-printed JSON of input arguments
	ToolID    string    // tool_use id for cross-turn matching
	ToolArgs  []ToolArg // key/value pairs parsed from the input object; nil for non-object inputs

	// For Type="tool_result"
	ToolUseID     string // matches a prior ToolID
	ResultContent string // tool result text content (truncated if long)
	ResultTrunc   bool   // true if ResultContent was truncated

	// For Type="image" or "document" — label only, no raw data
	MediaLabel string // e.g. "[image/jpeg]" or "[application/pdf]"
}

// ToolArg is a single key/value pair from a tool_use input object.
type ToolArg struct {
	Key   string
	Value string // possibly truncated to toolArgTruncateAt runes
	Trunc bool
}

// LogDetailData is the top-level struct passed to log_detail.html.
type LogDetailData struct {
	ActiveTab    string
	CurrentUser  string
	Log          *store.RequestLogDetail
	Conversation *ConversationData
	Cost         *CostBreakdown
}

const (
	truncateAt        = 500
	toolArgTruncateAt = 100
)

func truncateRunes(s string) (short string, full string, truncated bool) {
	r := []rune(s)
	if len(r) <= truncateAt {
		return s, "", false
	}
	return string(r[:truncateAt]), s, true
}

// parseConversation parses the stored request and response bodies into a
// ConversationData view model. It never returns nil; on any parse failure the
// ParseFailed flag is set and raw pretty-printed bodies are still populated.
func parseConversation(requestBody, responseBody string) *ConversationData {
	cd := &ConversationData{}

	// Always populate pretty-printed raw bodies.
	cd.RawRequest = prettyJSON(requestBody)
	cd.RawResponse = prettyJSON(responseBody)

	// Attempt to parse the request body as an Anthropic MessagesRequest.
	var req anthropic.MessagesRequest
	if err := json.Unmarshal([]byte(requestBody), &req); err != nil || len(req.Messages) == 0 {
		cd.ParseFailed = true
		return cd
	}

	// Normalise system prompt.
	cd.SystemPrompt = normaliseSystem(req.System)
	cd.HasSystem = cd.SystemPrompt != ""
	cd.SystemPromptLen = len([]rune(cd.SystemPrompt))

	// Build a map of tool_use_id → tool_name for resolving tool_result blocks.
	toolNameMap := make(map[string]string)
	for _, msg := range req.Messages {
		if msg.Role == "assistant" {
			for _, b := range extractContentBlocks(msg.Content) {
				if b.Type == "tool_use" && b.ID != "" && b.Name != "" {
					toolNameMap[b.ID] = b.Name
				}
			}
		}
	}

	// Walk messages: build History and identify LatestPrompt.
	lastUserIdx := -1
	for i, msg := range req.Messages {
		if msg.Role == "user" {
			lastUserIdx = i
		}
	}

	for i, msg := range req.Messages {
		turn := messageToTurn(msg, toolNameMap)
		if i == lastUserIdx {
			cd.LatestPrompt = &turn
		} else {
			cd.History = append(cd.History, turn)
		}
	}

	// Parse response body — try plain JSON first, then SSE streaming format.
	var respContent []anthropic.ContentBlock
	var directResp anthropic.MessagesResponse
	if err := json.Unmarshal([]byte(responseBody), &directResp); err == nil && len(directResp.Content) > 0 {
		respContent = directResp.Content
	} else if sse := parseSSEResponse(responseBody); sse != nil {
		respContent = sse
	}
	if len(respContent) > 0 {
		turn := contentBlocksToTurn("assistant", respContent, toolNameMap)
		cd.Response = &turn
	}

	return cd
}

// normaliseSystem converts the interface{} System field to a plain string.
func normaliseSystem(sys interface{}) string {
	if sys == nil {
		return ""
	}
	switch v := sys.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, item := range v {
			block, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			t, _ := block["type"].(string)
			switch t {
			case "text":
				if text, ok := block["text"].(string); ok {
					parts = append(parts, text)
				}
			default:
				parts = append(parts, "["+t+" block]")
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

// messageToTurn converts an anthropic.Message to a ConvTurn.
func messageToTurn(msg anthropic.Message, toolNameMap map[string]string) ConvTurn {
	blocks := extractContentBlocks(msg.Content)
	return contentBlocksToTurn(msg.Role, blocks, toolNameMap)
}

// contentBlocksToTurn renders a slice of ContentBlocks into a ConvTurn.
// Consecutive text blocks are merged into one to avoid a separate expand
// button for every injected context block (e.g. <system-reminder> entries).
func contentBlocksToTurn(role string, blocks []anthropic.ContentBlock, toolNameMap map[string]string) ConvTurn {
	turn := ConvTurn{Role: role}
	for _, b := range blocks {
		cb := contentBlockToConvBlock(b, toolNameMap)
		// Merge into the previous block if both are plain text.
		if cb.Type == "text" && len(turn.Blocks) > 0 && turn.Blocks[len(turn.Blocks)-1].Type == "text" {
			last := &turn.Blocks[len(turn.Blocks)-1]
			existing := last.FullText
			if existing == "" {
				existing = last.Text
			}
			incoming := cb.FullText
			if incoming == "" {
				incoming = cb.Text
			}
			merged := existing + "\n\n" + incoming
			short, full, trunc := truncateRunes(merged)
			last.Text = short
			last.Truncated = trunc
			last.FullText = full
			continue
		}
		turn.Blocks = append(turn.Blocks, cb)
	}
	return turn
}

// extractContentBlocks normalises the Message.Content interface{} to a
// []ContentBlock.  Content can be a raw string or a []interface{} of block maps.
func extractContentBlocks(content interface{}) []anthropic.ContentBlock {
	switch v := content.(type) {
	case string:
		return []anthropic.ContentBlock{{Type: "text", Text: v}}
	case []interface{}:
		var blocks []anthropic.ContentBlock
		for _, item := range v {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			b := anthropic.ContentBlock{
				Type: stringField(m, "type"),
				Text: stringField(m, "text"),
				ID:   stringField(m, "id"),
				Name: stringField(m, "name"),
			}
			if inp, ok := m["input"]; ok {
				b.Input = inp
			}
			// For tool_result, the tool_use_id is in "tool_use_id" key, not "id".
			if b.Type == "tool_result" {
				if toolUseID := stringField(m, "tool_use_id"); toolUseID != "" {
					b.ID = toolUseID
				}
				// tool_result content can be a string or array
				if c, ok := m["content"]; ok {
					switch cv := c.(type) {
					case string:
						b.Text = cv
					case []interface{}:
						// extract text from array of blocks
						var parts []string
						for _, ci := range cv {
							if cm, ok := ci.(map[string]interface{}); ok {
								if t := stringField(cm, "text"); t != "" {
									parts = append(parts, t)
								}
							}
						}
						b.Text = strings.Join(parts, "\n")
					}
				}
			}
			blocks = append(blocks, b)
		}
		return blocks
	default:
		return nil
	}
}

func stringField(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

// contentBlockToConvBlock converts a single ContentBlock to a ConvBlock.
func contentBlockToConvBlock(b anthropic.ContentBlock, toolNameMap map[string]string) ConvBlock {
	switch b.Type {
	case "text":
		short, full, trunc := truncateRunes(b.Text)
		return ConvBlock{
			Type:      "text",
			Text:      short,
			Truncated: trunc,
			FullText:  full,
		}

	case "tool_use":
		inputJSON := prettyJSON(marshalInput(b.Input))
		return ConvBlock{
			Type:      "tool_use",
			ToolName:  b.Name,
			ToolInput: inputJSON, // full JSON — no truncation at this level
			ToolID:    b.ID,
			ToolArgs:  parseToolArgs(b.Input),
		}

	case "tool_result":
		short, full, trunc := truncateRunes(b.Text)
		return ConvBlock{
			Type:          "tool_result",
			ToolUseID:     b.ID,
			ToolName:      toolNameMap[b.ID],
			ResultContent: short,
			ResultTrunc:   trunc,
			FullText:      full,
		}

	case "image":
		mediaType, _ := extractMediaType(b.Input)
		label := "[image"
		if mediaType != "" {
			label = "[" + mediaType
		}
		return ConvBlock{Type: "image", MediaLabel: label + "]"}

	case "document":
		mediaType, _ := extractMediaType(b.Input)
		label := "[document"
		if mediaType != "" {
			label = "[" + mediaType
		}
		return ConvBlock{Type: "document", MediaLabel: label + "]"}

	default:
		return ConvBlock{Type: "unknown", Text: "[" + b.Type + " block]"}
	}
}

func marshalInput(v interface{}) string {
	if v == nil {
		return "{}"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func extractMediaType(v interface{}) (string, bool) {
	m, ok := v.(map[string]interface{})
	if !ok {
		return "", false
	}
	mt, ok := m["media_type"].(string)
	return mt, ok
}

// parseToolArgs converts a tool input value into a sorted slice of ToolArg
// key/value pairs. Values are stringified and truncated to toolArgTruncateAt
// runes. Returns nil if the input is not a JSON object.
func parseToolArgs(input interface{}) []ToolArg {
	m, ok := input.(map[string]interface{})
	if !ok || len(m) == 0 {
		return nil
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	args := make([]ToolArg, 0, len(keys))
	for _, k := range keys {
		val := argValueString(m[k])
		r := []rune(val)
		if len(r) > toolArgTruncateAt {
			args = append(args, ToolArg{Key: k, Value: string(r[:toolArgTruncateAt]) + "…", Trunc: true})
		} else {
			args = append(args, ToolArg{Key: k, Value: val})
		}
	}
	return args
}

// argValueString converts a JSON value to a display string.
// Strings are returned as-is; other types are compact-JSON encoded.
func argValueString(v interface{}) string {
	if v == nil {
		return "null"
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// parseSSEResponse reconstructs content blocks from a captured SSE stream body.
// Returns nil if the body is not in SSE format or contains no content.
func parseSSEResponse(body string) []anthropic.ContentBlock {
	if !strings.Contains(body, "event:") || !strings.Contains(body, "data:") {
		return nil
	}

	type blockState struct {
		blockType string
		name      string
		id        string
		textBuf   strings.Builder
		inputBuf  strings.Builder
	}

	blocks := make(map[int]*blockState)
	var blockOrder []int

	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := line[6:]

		var envelope struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				Name string `json:"name"`
				ID   string `json:"id"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}

		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			continue
		}

		switch envelope.Type {
		case "content_block_start":
			bs := &blockState{
				blockType: envelope.ContentBlock.Type,
				name:      envelope.ContentBlock.Name,
				id:        envelope.ContentBlock.ID,
			}
			blocks[envelope.Index] = bs
			blockOrder = append(blockOrder, envelope.Index)
		case "content_block_delta":
			bs, ok := blocks[envelope.Index]
			if !ok {
				continue
			}
			switch envelope.Delta.Type {
			case "text_delta":
				bs.textBuf.WriteString(envelope.Delta.Text)
			case "input_json_delta":
				bs.inputBuf.WriteString(envelope.Delta.PartialJSON)
			}
		}
	}

	if len(blockOrder) == 0 {
		return nil
	}

	var result []anthropic.ContentBlock
	for _, idx := range blockOrder {
		bs := blocks[idx]
		switch bs.blockType {
		case "text":
			if text := bs.textBuf.String(); text != "" {
				result = append(result, anthropic.ContentBlock{Type: "text", Text: text})
			}
		case "tool_use":
			var input interface{}
			if raw := bs.inputBuf.String(); raw != "" {
				_ = json.Unmarshal([]byte(raw), &input)
			}
			result = append(result, anthropic.ContentBlock{
				Type:  "tool_use",
				Name:  bs.name,
				ID:    bs.id,
				Input: input,
			})
		}
	}

	return result
}

// prettyJSON re-indents a JSON string. Returns the original string if it
// cannot be parsed as JSON.
func prettyJSON(s string) string {
	if s == "" {
		return s
	}
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return s
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return s
	}
	return string(out)
}
