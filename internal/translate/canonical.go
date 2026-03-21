// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

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
