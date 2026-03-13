package translate

// ChatCompletionRequest is the OpenAI Chat Completions API request body.
type ChatCompletionRequest struct {
	Model           string         `json:"model"`
	Messages        []ChatMessage  `json:"messages"`
	MaxTokens       *int           `json:"max_tokens,omitempty"`
	Temperature     *float64       `json:"temperature,omitempty"`
	TopP            *float64       `json:"top_p,omitempty"`
	Stream          bool           `json:"stream,omitempty"`
	Stop            interface{}    `json:"stop,omitempty"` // string or []string
	Tools           []OpenAITool   `json:"tools,omitempty"`
	ToolChoice      interface{}    `json:"tool_choice,omitempty"`
	StreamOptions   *StreamOptions `json:"stream_options,omitempty"`
	ReasoningEffort *string        `json:"reasoning_effort,omitempty"`
}

// ChatMessage represents a single message in the OpenAI conversation format.
type ChatMessage struct {
	Role       string      `json:"role"`
	Content    interface{} `json:"content"` // string or []ContentPart
	Refusal    string      `json:"refusal,omitempty"`
	Name       string      `json:"name,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
}

// ImageURLContent holds the URL and optional detail level for an image_url content part.
type ImageURLContent struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// ContentPart represents a typed content element within a multimodal message.
type ContentPart struct {
	Type     string           `json:"type"`
	Text     string           `json:"text,omitempty"`
	ImageURL *ImageURLContent `json:"image_url,omitempty"`
}

// OpenAITool describes a tool available to the model in OpenAI format.
type OpenAITool struct {
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
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []Choice     `json:"choices"`
	Usage   *OpenAIUsage `json:"usage,omitempty"`
}

// Choice represents a single completion choice in the response.
type Choice struct {
	Index        int          `json:"index"`
	Message      *ChatMessage `json:"message,omitempty"`
	Delta        *ChatMessage `json:"delta,omitempty"`
	FinishReason *string      `json:"finish_reason"`
}

// OpenAIUsage reports token consumption in OpenAI format.
type OpenAIUsage struct {
	PromptTokens            int64                    `json:"prompt_tokens"`
	CompletionTokens        int64                    `json:"completion_tokens"`
	TotalTokens             int64                    `json:"total_tokens"`
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

// OpenAIErrorResponse wraps an error in OpenAI's error envelope.
type OpenAIErrorResponse struct {
	Error OpenAIError `json:"error"`
}

// OpenAIError contains the error details in OpenAI format.
type OpenAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}
