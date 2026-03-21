// SPDX-License-Identifier: AGPL-3.0-or-later

// Package openai implements the provider.Provider interface for OpenAI-compatible APIs.
package openai

import "encoding/json"

// APITypeChatCompletions is the api_type value for Chat Completions endpoints.
const APITypeChatCompletions = "chat_completions"

// APITypeResponses is the api_type value for Responses API endpoints.
const APITypeResponses = "responses"

// ---------------------------------------------------------------------------
// Chat Completions types
// ---------------------------------------------------------------------------

// ChatCompletionRequest is the OpenAI Chat Completions API request body.
type ChatCompletionRequest struct {
	Model           string         `json:"model"`
	Messages        []ChatMessage  `json:"messages"`
	MaxTokens       *int           `json:"max_tokens,omitempty"`
	Temperature     *float64       `json:"temperature,omitempty"`
	TopP            *float64       `json:"top_p,omitempty"`
	Stream          bool           `json:"stream,omitempty"`
	Stop            interface{}    `json:"stop,omitempty"` // string or []string
	Tools           []Tool         `json:"tools,omitempty"`
	ToolChoice      interface{}    `json:"tool_choice,omitempty"`
	StreamOptions   *StreamOptions `json:"stream_options,omitempty"`
	ReasoningEffort *string        `json:"reasoning_effort,omitempty"`
}

// ChatMessage represents a single message in the OpenAI conversation format.
type ChatMessage struct {
	Role             string      `json:"role,omitempty"`
	Content          interface{} `json:"content,omitempty"` // string or []ContentPart
	Refusal          string      `json:"refusal,omitempty"`
	Name             string      `json:"name,omitempty"`
	ToolCalls        []ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID       string      `json:"tool_call_id,omitempty"`
	Reasoning        string      `json:"reasoning,omitempty"`
	ReasoningContent string      `json:"reasoning_content,omitempty"`
}

// ImageURLContent holds the URL and optional detail level for an image_url content part.
type ImageURLContent struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// ContentPart represents a typed content element within a multimodal message.
type ContentPart struct {
	Type       string             `json:"type"`
	Text       string             `json:"text,omitempty"`
	ImageURL   *ImageURLContent   `json:"image_url,omitempty"`
	InputAudio *InputAudioContent `json:"input_audio,omitempty"`
	File       *FileContent       `json:"file,omitempty"`
}

// FileContent holds file data for a file content part.
type FileContent struct {
	FileData string `json:"file_data,omitempty"` // data URI: "data:application/pdf;base64,..."
	Filename string `json:"filename,omitempty"`
}

// InputAudioContent holds audio data in base64 format.
type InputAudioContent struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}

// Tool describes a tool available to the model in OpenAI format.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction holds a tool's name, description, and JSON Schema parameters.
type ToolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

// ToolCall represents a tool invocation returned by the model.
type ToolCall struct {
	Index    *int         `json:"index,omitempty"` // present in streaming chunks
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall holds the function name and serialized arguments for a tool call.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// StreamOptions controls optional streaming behavior.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// ChatCompletionResponse is the OpenAI Chat Completions API response.
type ChatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

// Choice represents a single completion choice in the response.
type Choice struct {
	Index        int          `json:"index"`
	Message      *ChatMessage `json:"message,omitempty"`
	Delta        *ChatMessage `json:"delta,omitempty"`
	FinishReason *string      `json:"finish_reason"`
}

// Usage reports token consumption in OpenAI format.
type Usage struct {
	PromptTokens            int64                    `json:"prompt_tokens"`
	CompletionTokens        int64                    `json:"completion_tokens"`
	TotalTokens             int64                    `json:"total_tokens"`
	ReasoningTokens         int64                    `json:"reasoning_tokens,omitempty"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}

// PromptTokensDetails holds detail breakdown of prompt tokens.
type PromptTokensDetails struct {
	CachedTokens int64 `json:"cached_tokens"`
}

// CompletionTokensDetails holds detail breakdown of completion tokens.
type CompletionTokensDetails struct {
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

// ErrorResponse wraps an error in OpenAI's error envelope.
type ErrorResponse struct {
	Error Error `json:"error"`
}

// Error contains the error details in OpenAI format.
type Error struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

// ---------------------------------------------------------------------------
// Responses API types
// ---------------------------------------------------------------------------

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

// modelsListResponse is the response from GET /v1/models.
type modelsListResponse struct {
	Data []openAIModelInfo `json:"data"`
}

// openAIModelInfo is a single model entry in the OpenAI listing response.
type openAIModelInfo struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by"`
}
