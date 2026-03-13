// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import "encoding/json"

// ResponsesRequest is the OpenAI Responses API request body.
type ResponsesRequest struct {
	Model              string            `json:"model"`
	Input              json.RawMessage   `json:"input,omitempty"`
	Instructions       *string           `json:"instructions,omitempty"`
	Temperature        *float64          `json:"temperature,omitempty"`
	TopP               *float64          `json:"top_p,omitempty"`
	MaxOutputTokens    *int              `json:"max_output_tokens,omitempty"`
	Stream             *bool             `json:"stream,omitempty"`
	Store              *bool             `json:"store,omitempty"`
	PreviousResponseID *string           `json:"previous_response_id,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	Tools              []ResponsesTool   `json:"tools,omitempty"`
	ToolChoice         interface{}       `json:"tool_choice,omitempty"`
	ParallelToolCalls  *bool             `json:"parallel_tool_calls,omitempty"`
	Truncation         *string           `json:"truncation,omitempty"`
	Reasoning          *Reasoning        `json:"reasoning,omitempty"`
	Text               *TextConfig       `json:"text,omitempty"`
	Include            []string          `json:"include,omitempty"`
	Background         *bool             `json:"background,omitempty"`
	ServiceTier        *string           `json:"service_tier,omitempty"`
}

// Reasoning holds reasoning model parameters.
type Reasoning struct {
	Effort  *string `json:"effort,omitempty"`
	Summary *string `json:"summary,omitempty"`
}

// TextConfig holds structured output configuration.
type TextConfig struct {
	Format *TextFormat `json:"format,omitempty"`
}

// TextFormat specifies the output format.
type TextFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name,omitempty"`
	Schema json.RawMessage `json:"schema,omitempty"`
	Strict *bool           `json:"strict,omitempty"`
}

// InputItem represents one element in the Responses API input array.
type InputItem struct {
	Type string `json:"type"`

	// input_text
	Text string `json:"text,omitempty"`

	// message
	Role    string          `json:"role,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`

	// input_image
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
	FileID   string `json:"file_id,omitempty"`

	// input_file
	FileData string `json:"file_data,omitempty"`
	FileURL  string `json:"file_url,omitempty"`
	Filename string `json:"filename,omitempty"`

	// input_audio
	Data   string `json:"data,omitempty"`
	Format string `json:"format,omitempty"`

	// function_call
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	// function_call_output
	Output string `json:"output,omitempty"`

	// item_reference
	ID string `json:"id,omitempty"`
}

// ResponsesTool is a tool definition in Responses API flat format.
type ResponsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

// ResponsesResponse is the Responses API response structure.
type ResponsesResponse struct {
	ID                string             `json:"id,omitempty"`
	Object            string             `json:"object"`
	CreatedAt         float64            `json:"created_at,omitempty"`
	Model             string             `json:"model,omitempty"`
	Status            string             `json:"status"`
	Output            []OutputItem       `json:"output,omitempty"`
	Usage             *ResponsesUsage    `json:"usage,omitempty"`
	Error             *ResponsesError    `json:"error,omitempty"`
	IncompleteDetails *IncompleteDetails `json:"incomplete_details,omitempty"`
	Instructions      *string            `json:"instructions,omitempty"`
	Metadata          map[string]string  `json:"metadata,omitempty"`
	Temperature       float64            `json:"temperature,omitempty"`
	TopP              float64            `json:"top_p,omitempty"`
	ToolChoice        interface{}        `json:"tool_choice,omitempty"`
	Tools             []ResponsesTool    `json:"tools,omitempty"`
}

// OutputItem represents one element in the Responses API output array.
type OutputItem struct {
	Type   string `json:"type"`
	ID     string `json:"id,omitempty"`
	Status string `json:"status,omitempty"`

	// message
	Role    string          `json:"role,omitempty"`
	Content []OutputContent `json:"content,omitempty"`

	// function_call
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	// reasoning
	Summary []ReasoningSummary `json:"summary,omitempty"`
}

// ReasoningSummary holds a single summary part within a reasoning output item.
type ReasoningSummary struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// OutputContent represents a content part within a message OutputItem.
type OutputContent struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Annotations []any  `json:"annotations,omitempty"`
	Refusal     string `json:"refusal,omitempty"`
}

// ResponsesUsage holds token accounting.
type ResponsesUsage struct {
	InputTokens         int64                `json:"input_tokens"`
	InputTokensDetails  *InputTokensDetails  `json:"input_tokens_details,omitempty"`
	OutputTokens        int64                `json:"output_tokens"`
	OutputTokensDetails *OutputTokensDetails `json:"output_tokens_details,omitempty"`
	TotalTokens         int64                `json:"total_tokens"`
}

// InputTokensDetails holds detail breakdown of input tokens.
type InputTokensDetails struct {
	CachedTokens int64 `json:"cached_tokens"`
}

// OutputTokensDetails holds detail breakdown of output tokens.
type OutputTokensDetails struct {
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

// ResponsesError holds error details.
type ResponsesError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// IncompleteDetails explains why a response is incomplete.
type IncompleteDetails struct {
	Reason string `json:"reason"`
}

// ResponsesStreamEvent is a generic SSE event envelope for Responses API streaming.
type ResponsesStreamEvent struct {
	Type     string          `json:"type"`
	Response json.RawMessage `json:"response,omitempty"`
}
