// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

// GeminiRequest is the request body for Vertex AI Gemini generateContent.
type GeminiRequest struct {
	Contents          []GeminiContent         `json:"contents"`
	SystemInstruction *GeminiContent          `json:"systemInstruction,omitempty"`
	GenerationConfig  *GeminiGenerationConfig `json:"generationConfig,omitempty"`
	Tools             []GeminiTool            `json:"tools,omitempty"`
	SafetySettings    []GeminiSafetySetting   `json:"safetySettings,omitempty"`
}

// GeminiContent represents a message in a conversation.
type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

// GeminiPart is a single segment of content within a message.
type GeminiPart struct {
	Text             string              `json:"text,omitempty"`
	InlineData       *GeminiBlob         `json:"inlineData,omitempty"`
	FunctionCall     *GeminiFunctionCall `json:"functionCall,omitempty"`
	FunctionResponse *GeminiFuncResponse `json:"functionResponse,omitempty"`
	Thought          *bool               `json:"thought,omitempty"`
	ThoughtSignature string              `json:"thoughtSignature,omitempty"`
}

// GeminiBlob holds inline binary data (images, etc.).
type GeminiBlob struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

// GeminiFunctionCall represents a function call predicted by the model.
type GeminiFunctionCall struct {
	Name string `json:"name"`
	Args any    `json:"args,omitempty"`
}

// GeminiFuncResponse is the result of a function call.
type GeminiFuncResponse struct {
	Name     string `json:"name"`
	Response any    `json:"response"`
}

// GeminiGenerationConfig holds generation parameters.
type GeminiGenerationConfig struct {
	MaxOutputTokens *int                  `json:"maxOutputTokens,omitempty"`
	Temperature     *float64              `json:"temperature,omitempty"`
	TopP            *float64              `json:"topP,omitempty"`
	TopK            *int                  `json:"topK,omitempty"`
	StopSequences   []string              `json:"stopSequences,omitempty"`
	ThinkingConfig  *GeminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

// GeminiThinkingConfig controls the model's thinking/reasoning.
type GeminiThinkingConfig struct {
	ThinkingBudget *int `json:"thinkingBudget,omitempty"`
}

// GeminiTool describes a tool available to the model.
type GeminiTool struct {
	FunctionDeclarations []GeminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

// GeminiFunctionDeclaration describes a single function the model can call.
type GeminiFunctionDeclaration struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

// GeminiSafetySetting configures content safety thresholds.
type GeminiSafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// GeminiResponse is the response from generateContent.
type GeminiResponse struct {
	Candidates    []GeminiCandidate    `json:"candidates"`
	UsageMetadata *GeminiUsageMetadata `json:"usageMetadata,omitempty"`
	ModelVersion  string               `json:"modelVersion,omitempty"`
}

// GeminiCandidate is a single generated response candidate.
type GeminiCandidate struct {
	Content       *GeminiContent       `json:"content,omitempty"`
	FinishReason  string               `json:"finishReason,omitempty"`
	SafetyRatings []GeminiSafetyRating `json:"safetyRatings,omitempty"`
}

// GeminiSafetyRating is a safety assessment for generated content.
type GeminiSafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
	Blocked     bool   `json:"blocked,omitempty"`
}

// GeminiUsageMetadata reports token consumption.
type GeminiUsageMetadata struct {
	PromptTokenCount        int64 `json:"promptTokenCount"`
	CachedContentTokenCount int64 `json:"cachedContentTokenCount,omitempty"`
	CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
	TotalTokenCount         int64 `json:"totalTokenCount"`
	ThoughtsTokenCount      int64 `json:"thoughtsTokenCount,omitempty"`
}

// geminiUnsupportedSchemaKeys lists JSON Schema keywords rejected by Gemini's
// OpenAPI-3.0-based Schema type. Any key starting with "$" is also dropped.
var geminiUnsupportedSchemaKeys = map[string]bool{
	"additionalProperties":  true,
	"definitions":           true,
	"propertyNames":         true,
	"const":                 true,
	"exclusiveMinimum":      true,
	"exclusiveMaximum":      true,
	"if":                    true,
	"then":                  true,
	"else":                  true,
	"not":                   true,
	"contains":              true,
	"unevaluatedProperties": true,
	"unevaluatedItems":      true,
	"prefixItems":           true,
	"dependentSchemas":      true,
	"dependentRequired":     true,
	"patternProperties":     true,
}

// sanitizeSchemaForGemini removes JSON Schema keywords not supported by the
// Gemini API's OpenAPI-3.0-based Schema type. It drops any field whose name
// starts with "$" (e.g. "$schema", "$defs") and all keys in
// geminiUnsupportedSchemaKeys. Operates recursively; non-map values are
// returned unchanged.
func sanitizeSchemaForGemini(schema any) any {
	switch v := schema.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, val := range v {
			if len(key) > 0 && key[0] == '$' {
				continue
			}
			if geminiUnsupportedSchemaKeys[key] {
				continue
			}
			out[key] = sanitizeSchemaForGemini(val)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = sanitizeSchemaForGemini(item)
		}
		return out
	default:
		return schema
	}
}
