package anthropic

// MessagesRequest is the body sent to POST /v1/messages.
type MessagesRequest struct {
	Model         string            `json:"model"`
	MaxTokens     int               `json:"max_tokens"`
	Messages      []Message         `json:"messages"`
	System        interface{}       `json:"system,omitempty"` // string or []SystemBlock
	Stream        bool              `json:"stream,omitempty"`
	Temperature   *float64          `json:"temperature,omitempty"`
	TopP          *float64          `json:"top_p,omitempty"`
	TopK          *int              `json:"top_k,omitempty"`
	StopSequences []string          `json:"stop_sequences,omitempty"`
	Tools         []Tool            `json:"tools,omitempty"`
	ToolChoice    interface{}       `json:"tool_choice,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Thinking      *ThinkingConfig   `json:"thinking,omitempty"`
}

// ThinkingConfig controls the extended thinking feature.
type ThinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

// Message represents a single message in the conversation.
type Message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []ContentBlock
}

// ImageSource holds the source data for an image content block.
type ImageSource struct {
	Type      string `json:"type"`                 // "base64" or "url"
	MediaType string `json:"media_type,omitempty"` // e.g. "image/jpeg"
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// CacheControl marks a content block as a cache breakpoint.
type CacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// ContentBlock represents a typed content element within a message.
type ContentBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	Thinking     string        `json:"thinking,omitempty"`
	ID           string        `json:"id,omitempty"`
	Name         string        `json:"name,omitempty"`
	Input        interface{}   `json:"input,omitempty"`
	Source       *ImageSource  `json:"source,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
	// tool_result fields
	ToolUseID string      `json:"tool_use_id,omitempty"`
	Content   interface{} `json:"content,omitempty"` // string or []ContentBlock
}

// Tool describes a tool available to the model.
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema interface{} `json:"input_schema"`
}

// MessagesResponse is the non-streaming response from POST /v1/messages.
type MessagesResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Content      []ContentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   *string        `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence,omitempty"`
	Usage        Usage          `json:"usage"`
}

// Usage reports token consumption for a request.
type Usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// SSEEvent is a parsed Server-Sent Event from the streaming API.
type SSEEvent struct {
	Event string
	Data  []byte
}

// MessageStartEvent is emitted at the beginning of a streaming response.
type MessageStartEvent struct {
	Type    string           `json:"type"`
	Message MessagesResponse `json:"message"`
}

// ContentBlockStartEvent signals the start of a new content block.
type ContentBlockStartEvent struct {
	Type         string       `json:"type"`
	Index        int          `json:"index"`
	ContentBlock ContentBlock `json:"content_block"`
}

// ContentBlockDeltaEvent carries an incremental update to a content block.
type ContentBlockDeltaEvent struct {
	Type  string     `json:"type"`
	Index int        `json:"index"`
	Delta DeltaBlock `json:"delta"`
}

// DeltaBlock holds the incremental content within a delta event.
type DeltaBlock struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

// MessageDeltaEvent carries final metadata at the end of a streaming response.
type MessageDeltaEvent struct {
	Type  string       `json:"type"`
	Delta MessageDelta `json:"delta"`
	Usage *DeltaUsage  `json:"usage,omitempty"`
}

// MessageDelta holds stop-related fields from a message_delta event.
type MessageDelta struct {
	StopReason   *string `json:"stop_reason"`
	StopSequence *string `json:"stop_sequence,omitempty"`
}

// DeltaUsage reports the final token counts in a streaming response message_delta event.
type DeltaUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// ErrorResponse is the Anthropic error envelope.
type ErrorResponse struct {
	Type  string      `json:"type"`
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains the error type and human-readable message.
type ErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// modelsListResponse is the response from GET /v1/models (direct API).
type modelsListResponse struct {
	Data    []modelInfo `json:"data"`
	HasMore bool        `json:"has_more"`
	LastID  string      `json:"last_id"`
}

// modelInfo is a single model entry in the listing response.
type modelInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// vertexModelsListResponse is the response from the Vertex AI publisher models endpoint.
type vertexModelsListResponse struct {
	PublisherModels []vertexPublisherModel `json:"publisherModels"`
	NextPageToken   string                 `json:"nextPageToken"`
}

// vertexPublisherModel is a model entry from the Vertex AI listing.
type vertexPublisherModel struct {
	Name string `json:"name"` // e.g. "publishers/anthropic/models/claude-sonnet-4-6"
}
